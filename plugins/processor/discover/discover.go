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
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "discover",
		Description: "actively search multiple backends for items from a title list; receives a title list from upstream source nodes and returns search results",
		Role:        plugin.RoleProcessor,
		// discover does not set fields itself; entries come from search sub-plugins
		// whose Produces/MayProduce are propagated by the DAG validator.
		Factory:       newPlugin,
		Validate:      validate,
		AcceptsSearch: true,
		Schema: []plugin.FieldSchema{
			{Key: "titles",   Type: plugin.FieldTypeList,     Hint: "Static title strings to search for (supplements upstream source nodes)"},
			{Key: "interval", Type: plugin.FieldTypeDuration, Hint: "Minimum time between re-searches per title (default 24h)"},
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
	errs = append(errs, plugin.OptUnknownKeys(cfg, "discover", "titles", "search", "interval")...)
	return errs
}

type discoverPlugin struct {
	titles    []string
	searchers []plugin.SearchPlugin
	interval  time.Duration
	db        *store.SQLiteStore
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
		titles:    titles,
		searchers: searchers,
		interval:  interval,
		db:        db,
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

func (p *discoverPlugin) Name() string        { return "discover" }

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
