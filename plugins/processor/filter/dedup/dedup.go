// Package dedup provides a processor plugin that removes duplicate entries for
// the same media item, keeping the best quality copy.
//
// "Best" is determined by:
//  1. Seed tier: entries with 2+ seeds beat entries with exactly 1 seed.
//  2. Resolution: higher resolution wins within the same tier.
//  3. Seeds: more seeds wins when tier and resolution are equal.
//
// media_type drives the classification: "series" entries dedup by series
// name + series_episode_id, "movie" entries dedup by title. Entries without
// media_type pass through unchanged.
//
// A name is not an identity, so two entries sharing one are only treated as
// copies of the same item when nothing proves they are different items: a
// provider id that disagrees, or (for movies) a release year that does. See
// [itemIDs.conflicts].
//
// Place dedup after a metainfo processor that sets media_type (typically
// metainfo_file, metainfo_tmdb, or metainfo_tvdb) and after filters that
// accept entries, before output sinks.
package dedup

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/quality"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "dedup",
		Description: "keep the best-quality copy when multiple entries refer to the same episode or movie",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalPerItem,
		Collapses:   true,
		// dedup only re-decides already-accepted entries — picking the best
		// among them. Undecided entries have no business being compared yet
		// (no upstream has chosen them as candidates), and rejected/failed
		// entries are off-limits.
		InputStates: entry.StatesAcceptedOnly,
		// media_type is the classifier; series_episode_id or title supplies
		// the key data. Together they encode media_type AND
		// (series_episode_id OR title) — enough signal to dedup either
		// kind of media. Entries without these still pass through, but
		// the validator surfaces a warning so users know dedup won't fire
		// on un-classified entries.
		Requires: [][]string{
			{entry.FieldMediaType},
			{entry.FieldSeriesEpisodeID, entry.FieldTitle},
		},
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) { return &dedupPlugin{}, nil },
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "dedup")
		},
	})
}

type dedupPlugin struct{}

func (p *dedupPlugin) Name() string { return "dedup" }

func (p *dedupPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	// Executor pre-filter (InputStates=StatesAcceptedOnly) means every entry
	// here is Accepted — no per-entry state check needed.
	//
	// Entries are grouped by name key, then split within a group into clusters
	// of entries nothing proves apart. A plain map keyed by a single string
	// cannot express that: "same unless proven different" is a comparison
	// between two entries, not a property of one.
	groups := map[string][]*cluster{}
	owner := map[*entry.Entry]*cluster{}
	for _, e := range entries {
		k := nameKey(e)
		if k == "" {
			continue
		}
		it := describe(e)
		c := pick(groups[k], it)
		if c == nil {
			c = &cluster{key: k}
			groups[k] = append(groups[k], c)
		}
		c.absorb(it)
		if c.best == nil || isBetter(e, c.best) {
			c.best = e
		}
		owner[e] = c
	}

	var out []*entry.Entry
	for _, e := range entries {
		c, keyed := owner[e]
		if !keyed {
			// Unkeyable entries (missing media_type or title) pass through
			// untouched — dedup has no opinion on them.
			out = append(out, e)
			continue
		}
		if c.best == e {
			out = append(out, e)
		} else {
			e.Reject(fmt.Sprintf("dedup: better copy already accepted for %q", c.key))
		}
	}
	return out, nil
}

// item is what is known about which media item an entry is a copy of, beyond
// the name it shares with the rest of its group.
type item struct {
	ids  itemIDs
	year int // movies only; series names are keyed with the year stripped
}

// cluster is a set of entries in one name group that nothing proves apart,
// plus the best of them so far. Its identity is the union of what its members
// published, so an entry that carries an id pins the cluster for the entries
// compared against it afterwards.
type cluster struct {
	key  string
	it   item
	best *entry.Entry
}

// pick returns the cluster an item belongs to, or nil for a new one. First fit:
// an entry that publishes nothing distinguishing joins the first cluster, which
// is what dedup did for every entry before clusters existed.
func pick(cs []*cluster, it item) *cluster {
	for _, c := range cs {
		if !c.it.ids.conflicts(it.ids) && yearsCompatible(c.it.year, it.year) {
			return c
		}
	}
	return nil
}

func (c *cluster) absorb(it item) {
	c.it.ids.absorb(it.ids)
	if c.it.year == 0 {
		c.it.year = it.year
	}
}

// itemIDs holds the provider ids an entry publishes, per namespace.
type itemIDs struct{ tvdb, tmdb, imdb string }

// conflicts reports whether two identities are provably different: some
// namespace where both name an id and the two ids differ. An id missing on
// either side proves nothing — most releases carry none at all, and splitting
// on absence would stop dedup working for exactly those.
func (a itemIDs) conflicts(b itemIDs) bool {
	return differ(a.tvdb, b.tvdb) || differ(a.tmdb, b.tmdb) || differ(a.imdb, b.imdb)
}

func (a *itemIDs) absorb(b itemIDs) {
	if a.tvdb == "" {
		a.tvdb = b.tvdb
	}
	if a.tmdb == "" {
		a.tmdb = b.tmdb
	}
	if a.imdb == "" {
		a.imdb = b.imdb
	}
}

func differ(x, y string) bool { return x != "" && y != "" && x != y }

// yearsCompatible mirrors match.YearsCompatible: unknown years are compatible
// with anything, and known years must be within one of each other, because a
// release names the year it was given and regional windows disagree by one.
func yearsCompatible(a, b int) bool {
	if a == 0 || b == 0 {
		return true
	}
	return a-b <= 1 && b-a <= 1
}

// describe reads the identity of the item an entry is a copy of.
func describe(e *entry.Entry) item {
	switch e.GetString(entry.FieldMediaType) {
	case entry.MediaTypeSeries:
		// Series ids only. A show's year is deliberately not compared: the
		// name key already has it stripped, because "Brothers 2026 S01E01"
		// and "Brothers S01E01" are one episode spelled two ways.
		return item{ids: itemIDs{tvdb: entry.TVDBID(e)}}
	case entry.MediaTypeMovie:
		return item{
			ids:  itemIDs{tmdb: entry.TMDBID(e), imdb: entry.IMDBID(e)},
			year: entry.ReleaseYear(e),
		}
	}
	return item{}
}

// nameKey groups the entries that might be copies of one another. It is
// deliberately loose — the year is stripped from a series name and absent from
// a movie title — and describe() supplies what separates the group again.
func nameKey(e *entry.Entry) string {
	switch e.GetString(entry.FieldMediaType) {
	case entry.MediaTypeSeries:
		epID := e.GetString(entry.FieldSeriesEpisodeID)
		if epID == "" {
			return ""
		}
		// Derive a normalised series name. Prefer parsing e.Title (the raw
		// entry title / torrent filename), which always yields a clean
		// series name like "Breaking Bad" regardless of which metainfo
		// plugin ran. Fall back to e.Fields["title"] when e.Title does not
		// parse as an episode (e.g. a list-sourced entry).
		//
		// The name is keyed without a trailing year: "Brothers 2026 S01E01"
		// and "Brothers S01E01 2026" are copies of the same episode.
		var name string
		if ep, ok := series.Parse(e.Title); ok {
			name = series.NewShow(ep.SeriesName, ep.SeriesYear).Base
		} else {
			name = series.NewShow(e.GetString(entry.FieldTitle), entry.ReleaseYear(e)).Base
		}
		if name == "" {
			return ""
		}
		return "episode:" + name + "/" + epID
	case entry.MediaTypeMovie:
		title := e.GetString(entry.FieldTitle)
		if title == "" {
			return ""
		}
		return "movie:" + strings.ToLower(title)
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
