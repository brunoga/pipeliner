// Package dedup provides a processor plugin that removes duplicate entries for
// the same media item, keeping the best quality copy.
//
// "Best" is determined by:
//  1. Seed tier: entries with 2+ seeds beat entries with exactly 1 seed.
//  2. Resolution: higher resolution wins within the same tier.
//  3. Seeds: more seeds wins when tier and resolution are equal.
//
// Episodes are keyed by series title + episode ID; movies by movie title.
// Entries without either key pass through unchanged.
//
// Place dedup after metainfo processors that set series_episode_id or
// movie_title and after filters that accept entries, before output sinks.
package dedup

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "dedup",
		Description: "keep the best-quality copy when multiple entries refer to the same episode or movie",
		Role:        plugin.RoleProcessor,
		Factory:     func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) { return &dedupPlugin{}, nil },
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "dedup")
		},
	})
}

type dedupPlugin struct{}

func (p *dedupPlugin) Name() string        { return "dedup" }

func (p *dedupPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	best := map[string]*entry.Entry{}

	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		k := deduKey(e)
		if k == "" {
			continue
		}
		if prev, ok := best[k]; !ok || isBetter(e, prev) {
			best[k] = e
		}
	}

	var out []*entry.Entry
	for _, e := range entries {
		if !e.IsAccepted() {
			out = append(out, e)
			continue
		}
		k := deduKey(e)
		if k == "" {
			out = append(out, e)
			continue
		}
		if best[k] == e {
			out = append(out, e)
		} else {
			reason := fmt.Sprintf("dedup: better copy already accepted for %q", k)
			e.Reject(reason)
			tc.Logger.Info("entry rejected", "entry", e.Title, "reason", reason)
		}
	}
	return out, nil
}

func deduKey(e *entry.Entry) string {
	if epID := e.GetString(entry.FieldSeriesEpisodeID); epID != "" {
		// Derive a normalised series name. Prefer parsing e.Title (the raw entry
		// title / torrent filename), which always gives a clean series name like
		// "Breaking Bad" regardless of whether metainfo_series has run.
		// Fall back to e.Fields["title"] when e.Title does not parse as an episode.
		var name string
		if ep, ok := series.Parse(e.Title); ok {
			name = ep.SeriesName
		} else {
			name = e.GetString(entry.FieldTitle)
		}
		if name != "" {
			return "episode:" + strings.ToLower(name) + "/" + epID
		}
	}
	if movie := e.GetString(entry.FieldMovieTitle); movie != "" {
		return "movie:" + strings.ToLower(movie)
	}
	return ""
}

func isBetter(a, b *entry.Entry) bool {
	seedsA, seedsB := seeds(a), seeds(b)
	tierA, tierB := seedTier(seedsA), seedTier(seedsB)
	if tierA != tierB {
		return tierA > tierB
	}
	resA := quality.Parse(a.Title).Resolution
	resB := quality.Parse(b.Title).Resolution
	if resA != resB {
		return resA > resB
	}
	return seedsA > seedsB
}

func seedTier(n int) int {
	if n >= 2 {
		return 1
	}
	return 0
}

func seeds(e *entry.Entry) int {
	v, ok := e.Get(entry.FieldTorrentSeeds)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		if n < 0 || n > math.MaxInt32 {
			return 0
		}
		return int(n)
	}
	return 0
}
