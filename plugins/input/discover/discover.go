// Package discover actively searches multiple backends for entries matching a
// title list, with a per-title cooldown to avoid redundant searches.
//
// It operates in two modes depending on whether it is used in a linear task
// or a DAG pipeline:
//
// Linear (InputPlugin): titles come from the 'titles' config key and/or from
// the 'from' list of sub-plugins. The plugin generates entries from scratch.
//
// DAG (ProcessorPlugin): upstream source nodes supply entries whose .Title
// fields form the search query list. Static 'titles' from config are also
// included. The plugin returns entries found by the search backends — not the
// upstream entries themselves.
//
// Config keys (both modes):
//
//	titles   - static list of title strings to search for (optional)
//	via      - list of search plugin configs (required); each entry is a name
//	           string or a map with "name" + plugin options
//	interval - minimum time between searches for the same title (default: "24h")
//
// Additional config key (linear mode only):
//
//	from     - list of input plugin configs whose entry titles supplement the
//	           title list (replaced by DAG upstream connections in DAG mode)
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
		Description: "actively search multiple backends for items from a title list; works as a source in linear tasks and as a processor in DAG pipelines",
		PluginPhase: plugin.PhaseInput, // retained for linear task engine backward compat
		Role:        plugin.RoleProcessor,
		Produces: []string{
			entry.FieldTorrentSeeds,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentLinkType,
		},
		Factory:  newPlugin,
		Validate: validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	viaRaw, _ := cfg["via"].([]any)
	if len(viaRaw) == 0 {
		errs = append(errs, fmt.Errorf("discover: \"via\" must list at least one search plugin"))
	}
	if err := plugin.OptDuration(cfg, "interval", "discover"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "discover", "titles", "from", "via", "interval")...)
	return errs
}

type discoverPlugin struct {
	titles    []string
	from      []plugin.InputPlugin
	searchers []plugin.SearchPlugin
	interval  time.Duration
	db        *store.SQLiteStore
}

type searchRecord struct {
	LastSearched time.Time `json:"last_searched"`
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	titles := toStringSlice(cfg["titles"])

	fromRaw, _ := cfg["from"].([]any)
	var froms []plugin.InputPlugin
	for _, item := range fromRaw {
		inp, err := plugin.MakeFromPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("discover: from: %w", err)
		}
		froms = append(froms, inp)
	}

	intervalStr, _ := cfg["interval"].(string)
	if intervalStr == "" {
		intervalStr = "24h"
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return nil, fmt.Errorf("discover: invalid interval %q: %w", intervalStr, err)
	}

	viaRaw, _ := cfg["via"].([]any)
	if len(viaRaw) == 0 {
		return nil, fmt.Errorf("discover: 'via' must list at least one search plugin")
	}
	var searchers []plugin.SearchPlugin
	for _, item := range viaRaw {
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
func (p *discoverPlugin) Phase() plugin.Phase { return plugin.PhaseInput }

// Run implements InputPlugin for the linear task engine. Titles come from the
// static list and from the 'from' sub-plugins configured in the plugin block.
func (p *discoverPlugin) Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	titles := append([]string{}, p.titles...)
	if len(p.from) > 0 {
		innerTC := &plugin.TaskContext{Name: tc.Name, Logger: tc.Logger}
		for _, inp := range p.from {
			fromEntries, err := inp.Run(ctx, innerTC)
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

// Process implements ProcessorPlugin for DAG pipelines. Upstream entries supply
// the title list (via their .Title field); static titles from config are also
// included. Returns entries found by the search backends, not the input entries.
func (p *discoverPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	titles := append([]string{}, p.titles...)
	for _, e := range entries {
		if e.Title != "" {
			titles = append(titles, e.Title)
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
