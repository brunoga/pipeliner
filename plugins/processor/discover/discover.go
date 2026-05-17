// Package discover actively searches multiple backends for entries matching a
// title list, with a per-title cooldown to avoid redundant searches.
//
// As a DAG processor, upstream source nodes supply the title list via their
// .Title fields. Static 'titles' from config and 'list' source plugins are
// also merged in. The plugin returns search results, not the upstream entries.
//
// Config keys:
//
//	titles   - static list of title strings to search for (optional)
//	list     - list of source plugin configs whose entry titles supplement the
//	           title list (alternative to DAG upstream connections)
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
		Produces: []string{
			entry.FieldTorrentSeeds,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentLinkType,
		},
		Factory:       newPlugin,
		Validate:      validate,
		AcceptsSearch: true,
		AcceptsList:   true,
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
	errs = append(errs, plugin.OptUnknownKeys(cfg, "discover", "titles", "list", "search", "interval")...)
	return errs
}

type discoverPlugin struct {
	titles    []string
	from      []plugin.SourcePlugin
	searchers []plugin.SearchPlugin
	interval  time.Duration
	db        *store.SQLiteStore
}

type searchRecord struct {
	LastSearched time.Time `json:"last_searched"`
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	titles := toStringSlice(cfg["titles"])

	listRaw, _ := cfg["list"].([]any)
	var froms []plugin.SourcePlugin
	for _, item := range listRaw {
		src, err := plugin.MakeListPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("discover: list: %w", err)
		}
		froms = append(froms, src)
	}

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
		from:      froms,
		searchers: searchers,
		interval:  interval,
		db:        db,
	}, nil
}

func resolveSearchPlugin(item any, db *store.SQLiteStore) (plugin.SearchPlugin, error) {
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
// the title list (via their .Title field); static titles from config and any
// 'from' source plugins are also included.
func (p *discoverPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	titles := append([]string{}, p.titles...)
	// Titles from upstream DAG entries.
	for _, e := range entries {
		if e.Title != "" {
			titles = append(titles, e.Title)
		}
	}
	// Titles from 'from' source plugins (config-based, for non-DAG callers).
	if len(p.from) > 0 {
		innerTC := &plugin.TaskContext{Name: tc.Name, Logger: tc.Logger}
		for _, src := range p.from {
			fromEntries, err := src.Generate(ctx, innerTC)
			if err != nil {
				continue
			}
			for _, e := range fromEntries {
				if e.Title != "" {
					titles = append(titles, e.Title)
				}
			}
		}
	}
	return p.searchTitles(ctx, tc, titles)
}

// searchTitles deduplicates the title list and dispatches searches via the
// configured search plugins, respecting the per-title interval cooldown.
func (p *discoverPlugin) searchTitles(ctx context.Context, tc *plugin.TaskContext, titles []string) ([]*entry.Entry, error) {
	// Deduplicate case-insensitively, preserve order.
	seenTitle := map[string]bool{}
	unique := titles[:0:0]
	for _, t := range titles {
		if key := strings.ToLower(t); !seenTitle[key] {
			seenTitle[key] = true
			unique = append(unique, t)
		}
	}
	titles = unique

	bucket := p.db.Bucket("discover:" + tc.Name)
	seen := map[string]bool{}
	var all []*entry.Entry

	for _, title := range titles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := strings.ToLower(title)

		var rec searchRecord
		found, err := bucket.Get(key, &rec)
		if err != nil {
			return nil, fmt.Errorf("discover: check interval for %q: %w", title, err)
		}
		if found && time.Since(rec.LastSearched) < p.interval {
			tc.Logger.Debug("discover: skipping (within interval)", "title", title)
			continue
		}

		for _, sp := range p.searchers {
			results, searchErr := sp.Search(ctx, tc, title)
			if searchErr != nil {
				tc.Logger.Warn("discover: search failed",
					"plugin", sp.Name(), "title", title, "err", searchErr)
				continue
			}
			for _, e := range results {
				if !seen[e.URL] {
					seen[e.URL] = true
					all = append(all, e)
				}
			}
		}

		if putErr := bucket.Put(key, searchRecord{LastSearched: time.Now().UTC()}); putErr != nil {
			tc.Logger.Warn("discover: update search timestamp failed", "title", title, "err", putErr)
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
