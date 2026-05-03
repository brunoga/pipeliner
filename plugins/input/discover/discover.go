// Package discover provides an input plugin that actively searches multiple
// sources for entries matching a list of titles.
//
// Unlike RSS-based inputs that passively receive entries, discover iterates a
// configured title list, dispatches a query to each configured search plugin,
// and returns the merged, deduplicated results. A per-title cooldown (interval)
// prevents redundant searches on successive runs.
//
// Config keys:
//
//	titles    - static list of title strings to search for
//	from      - list of input plugin configs whose entry titles supplement or
//	            replace the static list; same format as 'via' entries
//	via       - list of search plugin configs (required); each entry is either a
//	            plugin name string or a map with a "name" key plus plugin options
//	interval  - minimum time between searches for the same title (default: "24h")
//	db        - path to SQLite state file (default: "pipeliner.db")
//
// At least one of 'titles' or 'from' must produce titles.
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
		Description: "actively search multiple sources for items from a title list",
		PluginPhase: plugin.PhaseInput,
		Factory:     newPlugin,
	})
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

func newPlugin(cfg map[string]any) (plugin.Plugin, error) {
	titles := toStringSlice(cfg["titles"])

	fromRaw, _ := cfg["from"].([]any)
	var froms []plugin.InputPlugin
	for _, item := range fromRaw {
		inp, err := plugin.MakeInputPlugin(item)
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
		sp, err := resolveSearchPlugin(item)
		if err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}
		searchers = append(searchers, sp)
	}

	dbPath, _ := cfg["db"].(string)
	if dbPath == "" {
		dbPath = "pipeliner.db"
	}
	db, err := store.OpenSQLite(dbPath)
	if err != nil {
		return nil, fmt.Errorf("discover: open store: %w", err)
	}

	return &discoverPlugin{
		titles:    titles,
		from:      froms,
		searchers: searchers,
		interval:  interval,
		db:        db,
	}, nil
}

func resolveSearchPlugin(item any) (plugin.SearchPlugin, error) {
	name, pluginCfg, err := plugin.ResolveNameAndConfig(item)
	if err != nil {
		return nil, err
	}
	desc, ok := plugin.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown search plugin %q", name)
	}
	p, err := desc.Factory(pluginCfg)
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

func (p *discoverPlugin) Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	// Collect titles: static list + titles from 'from' input plugins.
	titles := append([]string{}, p.titles...)
	if len(p.from) > 0 {
		innerTC := &plugin.TaskContext{Name: tc.Name, Logger: tc.Logger}
		for _, inp := range p.from {
			fromEntries, err := inp.Run(ctx, innerTC)
			if err != nil {
				tc.Logger.Warn("discover: from source failed", "plugin", inp.Name(), "err", err)
				continue
			}
			for _, e := range fromEntries {
				if e.Title != "" {
					titles = append(titles, e.Title)
				}
			}
		}
	}

	// Deduplicate titles (case-insensitive) while preserving order.
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
