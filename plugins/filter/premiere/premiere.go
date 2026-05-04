// Package premiere provides a filter that accepts only the first episode of a
// series that has never been seen before, enabling automatic "try new shows"
// pipelines. A series is considered "seen" once its premiere has been accepted
// and persisted across runs via an SQLite-backed store.
//
// Requires metainfo/series (or filter/series) to have already run so that
// series_name, series_season, and series_episode are set on the entry.
//
// Config keys:
//
//	episode - episode number to treat as premiere (default: 1)
//	season  - season number to match; 0 means any season (default: 1)
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
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "premiere",
		Description: "accept only the first episode of series not previously seen (series premiere detection)",
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
	})
}

type premierePlugin struct {
	episode int
	season  int // 0 = any season
	db      *store.SQLiteStore
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	episode := intVal(cfg["episode"], 1)
	season := intVal(cfg["season"], 1)

	return &premierePlugin{episode: episode, season: season, db: db}, nil
}

func (p *premierePlugin) Name() string        { return "premiere" }
func (p *premierePlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *premierePlugin) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	seriesName := e.GetString("series_name")
	if seriesName == "" {
		e.Reject("premiere: series_name not set — run metainfo/series first")
		return nil
	}
	season := e.GetInt("series_season")
	episode := e.GetInt("series_episode")

	// Check season constraint.
	if p.season != 0 && season != p.season {
		e.Reject(fmt.Sprintf("premiere: season %d does not match premiere season %d", season, p.season))
		return nil
	}

	// Check episode number.
	if episode != p.episode {
		e.Reject(fmt.Sprintf("premiere: episode %d is not premiere episode %d", episode, p.episode))
		return nil
	}

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

	// New series premiere — accept and record it.
	rec = premiereRecord{
		SeriesName: seriesName,
		AcceptedAt: time.Now().UTC(),
		EntryURL:   e.URL,
	}
	if err := bucket.Put(key, rec); err != nil {
		tc.Logger.Warn("premiere: failed to persist record", "series", seriesName, "err", err)
	}
	e.Accept()
	return nil
}

// Learn is also implemented so the store write happens in the learn phase for
// consistency with other stateful plugins. The filter phase write above is
// kept as a belt-and-suspenders measure.
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
