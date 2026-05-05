package task

import (
	"fmt"
	"log/slog"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/quality"
)

// deduplicate ensures at most one accepted entry exists per unique media item.
// Episodes are keyed by series_name + series_episode_id; movies are keyed by
// movie_title. Entries with neither are not affected.
//
// Selection priority (lexicographic):
//  1. Seed tier: entries with 2+ seeds always beat entries with exactly 1 seed.
//  2. Resolution: higher resolution wins within the same tier.
//  3. Seeds: more seeds wins when tier and resolution are equal.
func deduplicate(entries []*entry.Entry, logger *slog.Logger) {
	best := map[string]*entry.Entry{}

	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		k := deduKey(e)
		if k == "" {
			continue
		}
		if prev, ok := best[k]; !ok || isBetterEntry(e, prev) {
			best[k] = e
		}
	}

	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		k := deduKey(e)
		if k == "" {
			continue
		}
		if best[k] != e {
			reason := fmt.Sprintf("dedup: better copy already accepted for %q", k)
			e.Reject(reason)
			logger.Debug("dedup: rejected duplicate",
				"rejected", e.Title,
				"kept", best[k].Title,
			)
		}
	}
}

// deduKey returns the deduplication key for an entry, or "" if not applicable.
func deduKey(e *entry.Entry) string {
	if series := e.GetString("series_name"); series != "" {
		if epID := e.GetString("series_episode_id"); epID != "" {
			return "episode:" + series + "/" + epID
		}
	}
	if movie := e.GetString("movie_title"); movie != "" {
		return "movie:" + movie
	}
	return ""
}

// isBetterEntry reports whether a is a better download candidate than b.
func isBetterEntry(a, b *entry.Entry) bool {
	seedsA, seedsB := entrySeeds(a), entrySeeds(b)

	// 1. Alive tier: 2+ seeds always beats 1 seed.
	tierA, tierB := seedTier(seedsA), seedTier(seedsB)
	if tierA != tierB {
		return tierA > tierB
	}

	// 2. Within the same tier: higher resolution wins.
	resA := quality.Parse(a.Title).Resolution
	resB := quality.Parse(b.Title).Resolution
	if resA != resB {
		return resA > resB
	}

	// 3. Same tier and resolution: more seeds wins.
	return seedsA > seedsB
}

// seedTier returns 1 for entries with 2+ seeds (reliable), 0 for 1 seed (risky).
func seedTier(seeds int) int {
	if seeds >= 2 {
		return 1
	}
	return 0
}

func entrySeeds(e *entry.Entry) int {
	if v, ok := e.Get("torrent_seeds"); ok {
		return anyToInt(v)
	}
	if v, ok := e.Get("torrent_seeders"); ok {
		return anyToInt(v)
	}
	return 0
}

func anyToInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
