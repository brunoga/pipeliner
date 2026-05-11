// Package premiere provides a filter that accepts premiere episodes of
// previously unseen series, enabling automatic "try new shows" pipelines.
//
// All qualifying entries for an unseen series are accepted — multiple
// quality variants from different sources pass through so the task engine's
// automatic deduplication can keep the best copy. A series is marked "seen"
// in the Learn phase (not Filter) so only the dedup survivor is recorded.
//
// Episode metadata is parsed directly from the entry title, so metainfo/series
// is not required. The parsed series_name, series_season, series_episode, and
// series_episode_id fields are set on the entry for use by downstream plugins.
//
// Config keys:
//
//	episode - episode number to treat as premiere (default: 1)
//	season  - season number to match; 0 means any season (default: 1)
//	quality - quality spec the entry must satisfy (e.g. "720p+ webrip+")
package premiere

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "premiere",
		Description: "accept only the first episode of series not previously seen (series premiere detection)",
		PluginPhase: plugin.PhaseFilter,
		Role:        plugin.RoleProcessor,
		Produces: []string{
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			entry.FieldSeriesEpisodeID,
		},
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "quality", Type: plugin.FieldTypeString, Hint: "Quality spec the entry must satisfy"},
			{Key: "episode", Type: plugin.FieldTypeInt, Default: 1, Hint: "Episode number to treat as premiere"},
			{Key: "season", Type: plugin.FieldTypeInt, Default: 1, Hint: "Season number to match (0 = any season)"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject entries that do not parse as episodes"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if q, _ := cfg["quality"].(string); q != "" {
		if _, err := quality.ParseSpec(q); err != nil {
			errs = append(errs, fmt.Errorf("premiere: invalid quality spec: %w", err))
		}
	}
	if v, ok := cfg["episode"]; ok {
		if n := intVal(v, -1); n < 0 {
			errs = append(errs, fmt.Errorf("premiere: \"episode\" must be a non-negative integer"))
		}
	}
	if v, ok := cfg["season"]; ok {
		if n := intVal(v, -1); n < 0 {
			errs = append(errs, fmt.Errorf("premiere: \"season\" must be a non-negative integer"))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "premiere", "episode", "season", "quality", "reject_unmatched")...)
	return errs
}

type premierePlugin struct {
	episode         int
	season          int // 0 = any season
	spec            quality.Spec
	tracker         *series.Tracker
	rejectUnmatched bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	episode := intVal(cfg["episode"], 1)
	season := intVal(cfg["season"], 1)

	var spec quality.Spec
	if q, _ := cfg["quality"].(string); q != "" {
		s, err := quality.ParseSpec(q)
		if err != nil {
			return nil, fmt.Errorf("premiere: invalid quality spec: %w", err)
		}
		spec = s
	}

	rejectUnmatched := true
	if v, ok := cfg["reject_unmatched"]; ok {
		rejectUnmatched, _ = v.(bool)
	}

	return &premierePlugin{
		episode:         episode,
		season:          season,
		spec:            spec,
		tracker:         series.NewTracker(db.Bucket("series")),
		rejectUnmatched: rejectUnmatched,
	}, nil
}

func (p *premierePlugin) Name() string        { return "premiere" }
func (p *premierePlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *premierePlugin) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	ep, ok := series.Parse(e.Title)
	if !ok {
		if p.rejectUnmatched {
			e.Reject("premiere: title did not parse as episode")
		}
		return nil
	}

	epID := series.EpisodeID(ep)
	e.Set("series_episode_id", epID)
	e.Set("series_season", ep.Season)
	e.Set("series_episode", ep.Episode)

	// Check season constraint.
	if p.season != 0 && ep.Season != p.season {
		e.Reject(fmt.Sprintf("premiere: season %d does not match premiere season %d", ep.Season, p.season))
		return nil
	}

	// Check episode number.
	if ep.Episode != p.episode {
		e.Reject(fmt.Sprintf("premiere: episode %d is not premiere episode %d", ep.Episode, p.episode))
		return nil
	}

	if (p.spec != quality.Spec{}) && !p.spec.Matches(ep.Quality) {
		e.Reject(fmt.Sprintf("premiere: %s %s quality %s does not match spec", ep.SeriesName, epID, ep.Quality.String()))
		return nil
	}

	seriesName := ep.SeriesName

	// Reject if this episode is already in the shared series tracker.
	if p.tracker.IsSeen(seriesName, epID) {
		e.Reject(fmt.Sprintf("premiere: %s %s already downloaded", seriesName, epID))
		return nil
	}

	// Accept all matching entries — dedup will keep the best one, Learn persists.
	e.Accept()
	return nil
}

// Learn persists the accepted premiere into the shared series tracker.
// Multiple entries for the same series may be accepted by Filter in the same
// run (different qualities/sources); the task engine deduplicates them before
// output and Learn only records the survivor.
func (p *premierePlugin) Learn(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		ep, ok := series.Parse(e.Title)
		if !ok {
			continue
		}
		epID := series.EpisodeID(ep)
		if p.tracker.IsSeen(ep.SeriesName, epID) {
			continue
		}
		_ = p.tracker.Mark(series.Record{
			SeriesName:   ep.SeriesName,
			EpisodeID:    epID,
			DownloadedAt: time.Now().UTC(),
			Quality:      ep.Quality,
		})
	}
	return nil
}

func intVal(v any, def int) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case int64:
		return int(t)
	}
	return def
}

func (p *premierePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.Filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("premiere filter error", "entry", e.Title, "err", err)
		}
	}
	out := entry.PassThrough(entries)
	if len(out) > 0 {
		if err := p.Learn(ctx, tc, out); err != nil {
			tc.Logger.Warn("premiere learn error", "err", err)
		}
	}
	return out, nil
}
