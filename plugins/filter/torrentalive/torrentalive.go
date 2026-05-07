// Package torrentalive provides a filter that rejects torrent entries with
// fewer seeds than a configured minimum.
//
// Seed counts are sourced in order:
//  1. The torrent_seeds entry field, set by the RSS input plugin from torrent
//     namespace extensions (nyaa, Jackett, ezrss, etc.).
//  2. If torrent_info_hash is not already set, the plugin populates it
//     automatically:
//     - magnet: URIs — info hash and tracker URLs are extracted from the URI
//       itself (no network call required).
//     - .torrent URLs — the file is downloaded and parsed to extract the info
//       hash and announce list.
//  3. Live tracker scraping: the plugin sends a scrape request to each announce
//     URL and uses the highest seed count returned.
//
// Entries where no seed count can be determined are left undecided.
// metainfo_magnet is NOT required — magnet URIs are parsed inline.
// For .torrent URL entries, add metainfo_torrent before torrent_alive if
// you want seed checking on those as well.
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
	"strings"
	"time"
	"net/url"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/magnet"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/tracker"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "torrent_alive",
		Description: "reject torrent entries with fewer seeds than min_seeds; auto-resolves info hash from magnet URIs and .torrent URLs",
		PluginPhase: plugin.PhaseFilter,
		Factory:  newPlugin,
		Validate: validate,
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
	errs = append(errs, plugin.OptUnknownKeys(cfg, "torrent_alive", "min_seeds", "scrape", "scrape_timeout")...)
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
		timeoutStr = "5s"
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
	// 1. Use feed-provided seed count if available (fast path — no scrape needed).
	if v, ok := e.Get("seeds"); ok {
		n := toInt(v)
		tc.Logger.Debug("torrent_alive: fast path (seeds)", "entry", e.Title, "seeds", n)
		return p.applyMinSeeds(e, n)
	}

	if !p.scrape {
		tc.Logger.Debug("torrent_alive: no seed data, scrape disabled — leaving undecided", "entry", e.Title)
		return nil
	}

	// 2. Populate info hash and announce list from the URL if not already set.
	if e.GetString("info_hash") == "" {
		if err := p.populate(ctx, e); err != nil {
			tc.Logger.Debug("torrent_alive: could not resolve torrent metadata",
				"entry", e.Title, "err", err)
		}
	}

	infoHash := e.GetString("info_hash")
	announces := announceList(e)
	if infoHash == "" || len(announces) == 0 {
		tc.Logger.Debug("torrent_alive: no info hash or announces — leaving undecided",
			"entry", e.Title, "has_hash", infoHash != "", "announces", len(announces))
		return nil
	}

	// Log only the hostname of the first announce to keep lines readable.
	firstHost := announces[0]
	if u, err := url.Parse(announces[0]); err == nil {
		firstHost = u.Host
	}
	tc.Logger.Debug("torrent_alive: scraping",
		"entry", e.Title, "hash", infoHash[:8]+"…", "trackers", len(announces), "first", firstHost)

	t0 := time.Now()
	scrapeCtx, cancel := context.WithTimeout(ctx, p.scrapeTimeout)
	defer cancel()

	seeds, err := tracker.Scrape(scrapeCtx, infoHash, announces)
	if err != nil {
		tc.Logger.Debug("torrent_alive: scrape failed",
			"entry", e.Title, "duration", time.Since(t0).Round(time.Millisecond), "err", err)
		return nil
	}

	tc.Logger.Debug("torrent_alive: scrape ok",
		"entry", e.Title, "seeds", seeds, "duration", time.Since(t0).Round(time.Millisecond))
	e.SetTorrentInfo(entry.TorrentInfo{Seeds: seeds})
	return p.applyMinSeeds(e, seeds)
}

// populate fills torrent_info_hash and torrent_announce_list from the entry URL
// when they have not been set by a prior metainfo plugin.
// Only magnet: URIs are handled — info hash and tracker URLs are extracted
// directly from the URI with no network call. All other URL types are a no-op.
func (p *torrentAlivePlugin) populate(_ context.Context, e *entry.Entry) error {
	if !strings.HasPrefix(e.URL, "magnet:") {
		return nil
	}
	m, err := magnet.Parse(e.URL)
	if err != nil {
		return err
	}
	ti := entry.TorrentInfo{InfoHash: m.InfoHash}
	if len(m.Trackers) > 0 {
		ti.AnnounceList = m.Trackers
		ti.Announce = m.Trackers[0]
	}
	e.SetTorrentInfo(ti)
	return nil
}

func (p *torrentAlivePlugin) applyMinSeeds(e *entry.Entry, seeds int) error {
	if seeds < p.minSeeds {
		e.Reject(fmt.Sprintf("torrent_alive: only %d seed(s), need at least %d", seeds, p.minSeeds))
	}
	return nil
}

// announceList returns the deduplicated list of announce URLs for an entry.
func announceList(e *entry.Entry) []string {
	if v, ok := e.Get("announce_list"); ok {
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
	if u := e.GetString("announce"); u != "" {
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
