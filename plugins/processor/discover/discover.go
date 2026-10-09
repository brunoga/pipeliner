// Package discover actively searches multiple backends for entries matching a
// title list, with a per-title cooldown to avoid redundant searches.
//
// As a DAG processor, upstream source nodes supply the title list via their
// .Title fields. Static 'titles' from config are also merged in. The plugin
// returns search results, not the upstream entries.
//
// Config keys:
//
//	titles   - static list of title strings to search for (optional)
//	search   - list of search plugin configs (required); each entry is a name
//	           string or a map with "name" + plugin options
//	interval - minimum time between searches for the same title (default: "24h")
package discover

import (
	"context"
	"strings"
	"time"

	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "discover",
		Description: "actively search multiple backends for items from a title list; receives a title list from upstream source nodes and returns search results",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalNone,
		// Entries come from the search sub-plugins, whose Produces/MayProduce
		// are propagated by the DAG validator. What discover sets itself is
		// the identity of the query that found the result, and only when the
		// query carried one — hence MayProduce.
		MayProduce:    []string{"tvdb_id", "tmdb_id", entry.FieldVideoImdbID},
		Factory:       newPlugin,
		Validate:      validate,
		AcceptsSearch: true,
		// Upstream entries supply titles to search for; discover returns brand
		// new entries built by the search backends. Without this hint the
		// per-source-entry result counters never see the new entries' state.
		ReplacesUpstream: true,
		Schema: []plugin.FieldSchema{
			{Key: "titles", Type: plugin.FieldTypeList, Hint: "Static title strings to search for (supplements upstream source nodes)"},
			{Key: "interval", Type: plugin.FieldTypeDuration, Hint: "Minimum time between re-searches per title (default 24h)"},
			{Key: "match_titles", Type: plugin.FieldTypeBool, Default: false, Hint: "Drop search results that do not match the query — indexer full-text search returns fuzzy junk. A series query is matched on show and episode; a movie query on title with a ±1 year window"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	searchRaw, _ := cfg["search"].([]any)
	if len(searchRaw) == 0 {
		errs = append(errs, fmt.Errorf("discover: \"search\" must list at least one search plugin"))
	}
	if err := plugin.OptDuration(cfg, "interval", "discover"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "discover", "titles", "search", "interval", "match_titles")...)
	return errs
}

type discoverPlugin struct {
	titles    []string
	searchers []plugin.SearchPlugin
	interval  time.Duration
	// matchTitles drops results whose parsed title doesn't match the query.
	// Off by default: some discovery flows want the fuzz (a curated indexer
	// where anything returned for the title is interesting); on-demand
	// request pipelines want it on, or "Mystery Men" downloads "Wake Up
	// Dead Man: A Knives Out Mystery".
	matchTitles bool
	db          *store.SQLiteStore
}

// searchRecord is the per-title bucket value. Results is intentionally
// declared without `omitempty` so an empty-but-non-nil slice serializes
// as "results":[] and a nil slice serializes as "results":null. That
// difference is load-bearing: pre-1.3.x records (and any record written
// before the cache feature shipped) have no "results" field at all and
// decode with Results==nil, which the read path treats as a cache miss
// so legacy records auto-heal on first access. A legitimate "we
// searched and got zero hits" cache writes []*entry.Entry{}, decodes as
// a non-nil empty slice, and is honored as a valid empty cache.
type searchRecord struct {
	LastSearched time.Time      `json:"last_searched"`
	Results      []*entry.Entry `json:"results"`
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	titles := toStringSlice(cfg["titles"])

	intervalStr, _ := cfg["interval"].(string)
	if intervalStr == "" {
		intervalStr = "24h"
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return nil, fmt.Errorf("discover: invalid interval %q: %w", intervalStr, err)
	}

	searchRaw, _ := cfg["search"].([]any)
	if len(searchRaw) == 0 {
		return nil, fmt.Errorf("discover: 'search' must list at least one search plugin")
	}
	var searchers []plugin.SearchPlugin
	for _, item := range searchRaw {
		sp, err := resolveSearchPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}
		searchers = append(searchers, sp)
	}

	return &discoverPlugin{
		titles:      titles,
		searchers:   searchers,
		interval:    interval,
		matchTitles: func() bool { b, _ := cfg["match_titles"].(bool); return b }(),
		db:          db,
	}, nil
}

func resolveSearchPlugin(item any, db *store.SQLiteStore) (plugin.SearchPlugin, error) {
	if p, ok := item.(*plugin.NodePipeline); ok {
		return plugin.MakeSearchPipeline(p, db)
	}
	name, pluginCfg, err := plugin.ResolveNameAndConfig(item)
	if err != nil {
		return nil, err
	}
	desc, ok := plugin.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown search plugin %q", name)
	}
	p, err := desc.Factory(pluginCfg, db)
	if err != nil {
		return nil, fmt.Errorf("instantiate search plugin %q: %w", name, err)
	}
	sp, ok := p.(plugin.SearchPlugin)
	if !ok {
		return nil, fmt.Errorf("plugin %q does not implement SearchPlugin", name)
	}
	return sp, nil
}

func (p *discoverPlugin) Name() string { return "discover" }

// Process implements ProcessorPlugin for DAG pipelines. Upstream entries supply
// the title list (via their .Title field) plus optional search hints in other
// fields (year, IDs, season/episode, …) that backends may use to refine the
// query. Static titles from config produce synthetic entries with only Title
// set, so they fall back to plain title-only searches.
func (p *discoverPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	// Upstream entries come first so their hint fields beat bare static-title
	// entries when both share a title.
	candidates := make([]*entry.Entry, 0, len(entries)+len(p.titles))
	for _, e := range entries {
		if e.Title != "" {
			candidates = append(candidates, e)
		}
	}
	for _, t := range p.titles {
		candidates = append(candidates, entry.New(t, ""))
	}
	return p.searchEntries(ctx, tc, candidates)
}

// searchEntries deduplicates the candidate list by lowercased title (preserving
// the first-seen entry, so hint-rich upstream entries beat bare static ones)
// and dispatches searches via the configured search plugins, respecting the
// per-title interval cooldown.
func (p *discoverPlugin) searchEntries(ctx context.Context, tc *plugin.TaskContext, candidates []*entry.Entry) ([]*entry.Entry, error) {
	seenTitle := map[string]bool{}
	unique := candidates[:0:0]
	for _, e := range candidates {
		key := strings.ToLower(e.Title)
		if !seenTitle[key] {
			seenTitle[key] = true
			unique = append(unique, e)
		}
	}

	bucket := p.db.Bucket("discover:" + tc.Name)
	seen := map[string]bool{}
	var all []*entry.Entry

	for _, qe := range unique {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := strings.ToLower(qe.Title)

		// Non-dry-run within-interval path: serve cached results so the
		// downstream pipeline still gets entries to work with even while
		// we're refusing to re-hit the indexers. Pre-1.3.x records have
		// no Results field — Get unmarshals that into a nil slice and the
		// title silently contributes nothing, matching the legacy
		// behaviour until the interval expires and we cache a fresh set.
		//
		// Dry-run bypasses the bucket entirely (read and write): the
		// debugging value of a dry-run is exercising the search backends
		// end-to-end, and we must not stamp every title as "just searched"
		// or the next real run within the TTL would silently no-op
		// (same idempotency principle #209 applied to the commit phase).
		if !tc.DryRun {
			var rec searchRecord
			found, err := bucket.Get(key, &rec)
			if err != nil {
				return nil, fmt.Errorf("discover: check cache for %q: %w", qe.Title, err)
			}
			// Legacy auto-heal: a pre-cache-feature record has a
			// LastSearched but no Results field. Treat it as a cache
			// miss so the title is re-searched immediately rather than
			// silently emitting nothing until the TTL expires.
			if found && rec.Results != nil && time.Since(rec.LastSearched) < p.interval {
				tc.Logger.Debug("discover: serving cached results (within interval)",
					"title", qe.Title, "count", len(rec.Results))
				for _, e := range rec.Results {
					if e == nil || seen[e.URL] {
						continue
					}
					if p.matchTitles && !matchesQuery(qe.Title, e.Title) {
						continue // cached before the option was enabled
					}
					stampQueryIdentity(qe, e) // cached before the id was carried
					seen[e.URL] = true
					all = append(all, e)
				}
				continue
			}
		}

		// Fresh search: collect each searcher's results into a per-title
		// slice so we can cache the complete set for this title, then
		// apply cross-title URL dedup when assembling the output.
		// Initialized non-nil so a zero-result cache round-trips as []
		// rather than null — see searchRecord doc for why that matters.
		titleResults := []*entry.Entry{}
		for _, sp := range p.searchers {
			results, searchErr := sp.Search(ctx, tc, qe)
			if searchErr != nil {
				tc.Logger.Warn("discover: search failed",
					"plugin", sp.Name(), "title", qe.Title, "err", searchErr)
				continue
			}
			titleResults = append(titleResults, results...)
		}
		if p.matchTitles {
			kept := titleResults[:0]
			for _, e := range titleResults {
				if e == nil {
					continue
				}
				if !matchesQuery(qe.Title, e.Title) {
					tc.Logger.Debug("discover: dropping non-matching result",
						"query", qe.Title, "result", e.Title)
					continue
				}
				kept = append(kept, e)
			}
			titleResults = kept
		}
		for _, e := range titleResults {
			if e != nil {
				stampQueryIdentity(qe, e)
			}
		}
		for _, e := range titleResults {
			if e == nil || seen[e.URL] {
				continue
			}
			seen[e.URL] = true
			all = append(all, e)
		}

		if !tc.DryRun {
			rec := searchRecord{LastSearched: time.Now().UTC(), Results: titleResults}
			if putErr := bucket.Put(key, rec); putErr != nil {
				tc.Logger.Warn("discover: update search cache failed", "title", qe.Title, "err", putErr)
			}
		}
	}
	return all, nil
}

// identityFields are the provider namespaces a result inherits from the query
// that found it, each read from wherever the query happens to carry it and
// written under pipeliner's own field name for that namespace.
var identityFields = []struct {
	field string
	read  func(*entry.Entry) string
}{
	{"tvdb_id", entry.TVDBID},
	{"tmdb_id", entry.TMDBID},
	{entry.FieldVideoImdbID, entry.IMDBID},
}

// stampQueryIdentity copies the identity of the query onto a result found for
// it. An indexer returns a release name and nothing else, and two items can
// share one — a downstream filter or metainfo plugin needs to know which of
// them we were actually searching for, rather than guessing from the name all
// over again. Only a query that carries an id contributes one, and a backend
// that already identified the result keeps its own answer.
//
// Results are stamped per query, before the cross-query URL dedup, so the
// identity belongs to the query that first claimed the release.
func stampQueryIdentity(qe, result *entry.Entry) {
	for _, id := range identityFields {
		if v := id.read(qe); v != "" && id.read(result) == "" {
			result.Set(id.field, v)
		}
	}
}

func toStringSlice(v any) []string {
	switch val := v.(type) {
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{val}
	}
	return nil
}

// matchesQuery reports whether a search result's release name is actually
// the queried title. Indexer full-text search happily returns "Wake Up Dead
// Man: A Knives Out Mystery" for the query "Mystery Men"; parsing both sides
// with the movie release-name parser and requiring normalized-title equality
// (plus year compatibility when both sides know one) keeps only real hits.
// matchesSeriesQuery compares a parsed series query against a release name:
// same show, same episode. Show identity goes through series.Show, so a
// release naming the premiere year ("Brothers 2026") matches a query that does
// not ("Brothers") -- the same rule the tracker and the series filter use.
func matchesSeriesQuery(q *series.Episode, releaseName string) bool {
	r, ok := series.Parse(releaseName)
	if !ok || r.SeriesName == "" {
		// A season pack answering an episode query lands here, as does junk.
		// Either way a downstream require(series_episode_id) would drop it.
		return false
	}
	if !series.NewShow(q.SeriesName, q.SeriesYear).Matches(series.NewShow(r.SeriesName, r.SeriesYear)) {
		return false
	}
	if q.IsDate || r.IsDate {
		return q.IsDate && r.IsDate && q.Year == r.Year && q.Month == r.Month && q.Day == r.Day
	}
	if q.Season != r.Season {
		return false
	}
	if q.Episode == r.Episode {
		return true
	}
	// A double release covering the episode asked for is still that episode.
	return r.DoubleEpisode > 0 && q.Episode > r.Episode && q.Episode <= r.DoubleEpisode
}

func matchesQuery(query, releaseName string) bool {
	// A series query names an episode, and the movie-shaped comparison below
	// cannot read one: it parses "Show S01E01" as a film title and compares it
	// to a release name that carries an episode title and tags, so good
	// releases are dropped. Measured: every result for
	// "Tomb Raider: The Legend of Lara Croft S01E01" was discarded, because
	// the colon survives one parse and not the other.
	//
	// It also could not do the one check worth doing here -- a query for S03E05
	// used to accept S03E06, since only the title was compared.
	if q, ok := series.Parse(query); ok && q.SeriesName != "" {
		return matchesSeriesQuery(q, releaseName)
	}

	qTitle, qYear := query, 0
	if mv, ok := movies.Parse(query); ok && mv.Title != "" {
		qTitle, qYear = mv.Title, mv.Year
	}
	rTitle, rYear := releaseName, 0
	if mv, ok := movies.Parse(releaseName); ok && mv.Title != "" {
		rTitle, rYear = mv.Title, mv.Year
	}
	if movies.NormalizeTitle(qTitle) != movies.NormalizeTitle(rTitle) {
		return false
	}
	if qYear != 0 && rYear != 0 {
		d := qYear - rYear
		if d < -1 || d > 1 {
			return false
		}
	}
	return true
}
