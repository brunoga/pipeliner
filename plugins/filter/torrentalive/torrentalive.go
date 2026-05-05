// Package torrentalive provides a filter that rejects torrent entries with
// fewer seeds than a configured minimum.
//
// Seed counts are sourced in order:
//  1. The torrent_seeds entry field, set by the RSS input plugin from torrent
//     namespace extensions (nyaa, Jackett, ezrss, etc.).
//  2. Live tracker scraping: when torrent_seeds is absent and scrape is enabled
//     (default), the plugin queries the announce URLs in torrent_announce_list
//     (or torrent_announce) using the HTTP scrape convention or UDP tracker
//     protocol. Only entries that also have torrent_info_hash are scraped.
//
// Entries where no seed count can be determined are left undecided.
//
// Config keys:
//
//	min_seeds      - minimum acceptable seed count (default: 1)
//	scrape         - enable live tracker scraping when torrent_seeds absent (default: true)
//	scrape_timeout - per-scrape deadline, e.g. "10s" (default: "15s")
package torrentalive

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/tracker"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "torrent_alive",
		Description: "reject torrent entries with fewer seeds than min_seeds; optionally scrapes trackers",
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if v, ok := cfg["min_seeds"]; ok {
		if n := intVal(v, 0); n < 1 {
			errs = append(errs, fmt.Errorf("torrent_alive: \"min_seeds\" must be at least 1"))
		}
	}
	if err := plugin.OptDuration(cfg, "scrape_timeout", "torrent_alive"); err != nil {
		errs = append(errs, err)
	}
	return errs
}

type torrentAlivePlugin struct {
	minSeeds      int
	scrape        bool
	scrapeTimeout time.Duration
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	min := intVal(cfg["min_seeds"], 1)
	if min < 1 {
		return nil, fmt.Errorf("torrent_alive: min_seeds must be at least 1")
	}

	scrapeEnabled := true
	if v, ok := cfg["scrape"].(bool); ok {
		scrapeEnabled = v
	}

	timeoutStr, _ := cfg["scrape_timeout"].(string)
	if timeoutStr == "" {
		timeoutStr = "15s"
	}
	scrapeTimeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, fmt.Errorf("torrent_alive: invalid scrape_timeout %q: %w", timeoutStr, err)
	}

	return &torrentAlivePlugin{
		minSeeds:      min,
		scrape:        scrapeEnabled,
		scrapeTimeout: scrapeTimeout,
	}, nil
}

func (p *torrentAlivePlugin) Name() string        { return "torrent_alive" }
func (p *torrentAlivePlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *torrentAlivePlugin) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	// 1. Use RSS-provided seed count if available.
	if v, ok := e.Get("torrent_seeds"); ok {
		return p.applyMinSeeds(e, toInt(v))
	}

	// 2. Optionally scrape trackers.
	if !p.scrape {
		return nil // no seed count available; don't reject
	}

	infoHash := e.GetString("torrent_info_hash")
	announces := announceList(e)
	if infoHash == "" || len(announces) == 0 {
		return nil // not enough info to scrape; leave undecided
	}

	scrapeCtx, cancel := context.WithTimeout(ctx, p.scrapeTimeout)
	defer cancel()

	seeds, err := tracker.Scrape(scrapeCtx, infoHash, announces)
	if err != nil {
		// Tracker unreachable — leave undecided rather than reject.
		if tc.Logger != nil {
			tc.Logger.Debug("torrent_alive: scrape failed, skipping filter",
				"entry", e.Title, "err", err)
		}
		return nil
	}

	// Successfully scraped; write back so downstream plugins can see it.
	e.Set("torrent_seeds", seeds)
	return p.applyMinSeeds(e, seeds)
}

func (p *torrentAlivePlugin) applyMinSeeds(e *entry.Entry, seeds int) error {
	if seeds < p.minSeeds {
		e.Reject(fmt.Sprintf("torrent_alive: only %d seed(s), need at least %d", seeds, p.minSeeds))
	}
	return nil
}

// announceList returns the deduplicated list of announce URLs for an entry,
// reading from torrent_announce_list ([]string) then falling back to the
// torrent_announce string field.
func announceList(e *entry.Entry) []string {
	if v, ok := e.Get("torrent_announce_list"); ok {
		switch t := v.(type) {
		case []string:
			if len(t) > 0 {
				return t
			}
		case []any:
			var out []string
			for _, item := range t {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	if u := e.GetString("torrent_announce"); u != "" {
		return []string{u}
	}
	return nil
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

func toInt(v any) int {
	return intVal(v, 0)
}
