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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
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
		Factory:     newPlugin,
		Validate:    validate,
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
	errs = append(errs, plugin.OptUnknownKeys(cfg, "premiere", "episode", "season", "quality")...)
	return errs
}

type premierePlugin struct {
	episode int
	season  int // 0 = any season
	spec    quality.Spec
	db      *store.SQLiteStore
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

	return &premierePlugin{episode: episode, season: season, spec: spec, db: db}, nil
}

func (p *premierePlugin) Name() string        { return "premiere" }
func (p *premierePlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *premierePlugin) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	ep, ok := series.Parse(e.Title)
	if !ok {
		tc.Logger.Debug("premiere: title did not parse as episode", "entry", e.Title)
		return nil
	}

	epID := series.EpisodeID(ep)
	e.Set("series_name", ep.SeriesName)
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

	// Check if this series has already had its premiere accepted.
	key := normalize(seriesName)
	bucket := p.db.Bucket("premiere:" + tc.Name)
	var rec premiereRecord
	found, err := bucket.Get(key, &rec)
	if err != nil {
		tc.Logger.Warn("premiere: store lookup failed", "series", seriesName, "err", err)
	}
	if found {
		e.Reject(fmt.Sprintf("premiere: series %q premiere already accepted on %s", seriesName, rec.AcceptedAt.Format("2006-01-02")))
		return nil
	}

	// Accept all matching entries — dedup will keep the best one, Learn persists.
	e.Accept()
	return nil
}

// Learn persists the accepted premiere. Multiple entries for the same series
// may be accepted by Filter in the same run (different qualities/sources);
// the task engine deduplicates them before output and Learn only records the
// survivor.
func (p *premierePlugin) Learn(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	bucket := p.db.Bucket("premiere:" + tc.Name)
	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		seriesName := e.GetString("series_name")
		if seriesName == "" {
			continue
		}
		key := normalize(seriesName)
		var existing premiereRecord
		if found, _ := bucket.Get(key, &existing); found {
			continue // already recorded
		}
		rec := premiereRecord{
			SeriesName: seriesName,
			AcceptedAt: time.Now().UTC(),
			EntryURL:   e.URL,
		}
		_ = bucket.Put(key, rec)
	}
	return nil
}

type premiereRecord struct {
	SeriesName string    `json:"series_name"`
	AcceptedAt time.Time `json:"accepted_at"`
	EntryURL   string    `json:"entry_url"`
}

// Ensure premiereRecord round-trips through json (used by bucket store).
var _ json.Marshaler = (*premiereRecord)(nil)

func (r premiereRecord) MarshalJSON() ([]byte, error) {
	type alias premiereRecord
	return json.Marshal(alias(r))
}

func normalize(s string) string {
	return strings.ToLower(match.Normalize(s))
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
