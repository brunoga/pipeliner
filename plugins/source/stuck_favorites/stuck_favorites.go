// Package stuck_favorites emits one entry per "stuck" favorite: a show or movie
// on a list whose candidates keep appearing in runs but never get accepted.
// Pipe it into a notify sink on a schedule to be alerted when something silently
// stops downloading — the failure mode behind the Star Trek: Strange New Worlds
// incident, where an episode failed to grab for weeks with nothing to flag it.
//
// It reads the resolved favorite lists from the series/movies title-list caches
// and correlates them with the kept run traces via the watchdog package.
package stuck_favorites

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/traces"
	"github.com/brunoga/pipeliner/internal/watchdog"
)

const pluginName = "stuck_favorites"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "emit one entry per favorite whose candidates keep being rejected across runs (never accepted)",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTitle, "stuck_favorite", "stuck_runs", "stuck_nearest_distance",
		},
		MayProduce: []string{"stuck_last_reason", "stuck_example_title", "stuck_last_task"},
		Factory:    newPlugin,
		Validate:   validate,
		Schema: []plugin.FieldSchema{
			{Key: "min_runs", Type: plugin.FieldTypeInt, Default: watchdog.DefaultMinRuns, Hint: "Distinct runs a favorite must be seen in (never accepted) before it is reported"},
			{Key: "max_distance", Type: plugin.FieldTypeInt, Default: watchdog.DefaultMaxDistance, Hint: "Max edit distance to link a candidate title to a favorite (tolerates normalization gaps)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	return plugin.OptUnknownKeys(cfg, pluginName, "min_runs", "max_distance")
}

type stuckPlugin struct {
	store       *traces.Store
	db          *store.SQLiteStore
	minRuns     int
	maxDistance int
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	if db == nil {
		return nil, fmt.Errorf("%s: requires the store", pluginName)
	}
	return &stuckPlugin{
		store:       traces.NewStore(db.Bucket(traces.BucketName)),
		db:          db,
		minRuns:     intVal(cfg["min_runs"], watchdog.DefaultMinRuns),
		maxDistance: intVal(cfg["max_distance"], watchdog.DefaultMaxDistance),
	}, nil
}

func (p *stuckPlugin) Name() string { return pluginName }

// favorites unions the title lists cached by the series and movies filters,
// tagging each with its media kind so correlation stays scoped to the pipelines
// that use it.
func (p *stuckPlugin) favorites() []watchdog.Favorite {
	var out []watchdog.Favorite
	for _, b := range []struct {
		bucket string
		kind   watchdog.MediaKind
	}{
		{"cache_series_list", watchdog.KindSeries},
		{"cache_movies_list", watchdog.KindMovies},
	} {
		if lists, ok := cache.Values[[]match.TitleEntry](p.db.Bucket(b.bucket)); ok {
			for _, l := range lists {
				out = append(out, watchdog.MakeFavorites(l, b.kind)...)
			}
		}
	}
	return out
}

// Generate implements plugin.SourcePlugin.
func (p *stuckPlugin) Generate(_ context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	occ, err := p.store.AllOccurrences()
	if err != nil {
		return nil, fmt.Errorf("%s: read traces: %w", pluginName, err)
	}
	taskKinds, err := watchdog.LoadTaskKinds(p.db.Bucket(watchdog.TaskKindsBucket))
	if err != nil {
		return nil, fmt.Errorf("%s: read task kinds: %w", pluginName, err)
	}
	stuck := watchdog.Detect(occ, p.favorites(), taskKinds, p.minRuns, p.maxDistance)

	out := make([]*entry.Entry, 0, len(stuck))
	for _, s := range stuck {
		e := entry.New(
			fmt.Sprintf("Stuck favorite: %s (seen in %d runs, never downloaded)", s.Favorite, s.Runs),
			fmt.Sprintf("pipeliner://stuck/%s", s.Favorite),
		)
		e.Fields["stuck_favorite"] = s.Favorite
		e.Fields["stuck_runs"] = s.Runs
		e.Fields["stuck_nearest_distance"] = s.NearestDistance
		if s.LastReason != "" {
			e.Fields["stuck_last_reason"] = s.LastReason
		}
		if s.ExampleTitle != "" {
			e.Fields["stuck_example_title"] = s.ExampleTitle
		}
		if s.LastTask != "" {
			e.Fields["stuck_last_task"] = s.LastTask
		}
		out = append(out, e)
	}
	tc.Logger.Info(pluginName+": favorites reported", "count", len(out), "min_runs", p.minRuns)
	return out, nil
}

func intVal(v any, def int) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return def
}
