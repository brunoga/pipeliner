// Package torrentalive provides a filter that rejects torrent entries with
// fewer seeds than a configured minimum.
//
// Seed counts are sourced in order:
//  1. The torrent_seeds entry field, set by the RSS input plugin from torrent
//     namespace extensions (nyaa, Jackett, ezrss, etc.). Skipped when
//     verify=true — indexer-reported counts can be stale or phantom
//     (especially for rare releases), so verify forces a live scrape and
//     only falls back to the feed count when scraping is impossible.
//  2. If torrent_info_hash is not already set, the plugin extracts it inline
//     from magnet: URIs (no network call). For .torrent URL entries, add
//     metainfo_torrent before torrent_alive so the hash and announce list
//     are available.
//  3. Live tracker scraping: the plugin sends a scrape request to each announce
//     URL and uses the highest seed count returned.
//
// Entries where no seed count can be determined are left undecided.
//
// Config keys:
//
//	min_seeds      - minimum acceptable seed count (default: 1)
//	scrape         - enable live tracker scraping when torrent_seeds absent (default: true)
//	verify         - always scrape to verify feed-provided seed counts (default: false)
//	scrape_timeout - per-scrape deadline, e.g. "10s" (default: "15s")
package torrentalive

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

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
		Role:        plugin.RoleProcessor,
		// torrent_seeds only set when a seed count is successfully resolved.
		// torrent_leechers is never populated by this plugin.
		MayProduce: []string{
			entry.FieldTorrentSeeds,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "min_seeds", Type: plugin.FieldTypeInt, Default: 1, Hint: "Minimum seed count"},
			{Key: "scrape", Type: plugin.FieldTypeBool, Default: true, Hint: "Scrape tracker when seed count unknown"},
			{Key: "verify", Type: plugin.FieldTypeBool, Default: false, Hint: "Always scrape to verify feed-provided seed counts (falls back to the feed count when scraping is impossible)"},
			{Key: "scrape_timeout", Type: plugin.FieldTypeDuration, Default: "15s", Hint: "Per-scrape deadline"},
		},
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
	if v, ok := cfg["verify"].(bool); ok && v {
		if s, ok := cfg["scrape"].(bool); ok && !s {
			errs = append(errs, fmt.Errorf("torrent_alive: \"verify\" requires scraping; remove \"scrape\": false"))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "torrent_alive", "min_seeds", "scrape", "verify", "scrape_timeout")...)
	return errs
}

type torrentAlivePlugin struct {
	minSeeds      int
	scrape        bool
	verify        bool
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

	verify, _ := cfg["verify"].(bool)
	if verify && !scrapeEnabled {
		return nil, fmt.Errorf("torrent_alive: verify requires scraping; remove \"scrape\": false")
	}

	return &torrentAlivePlugin{
		minSeeds:      min,
		scrape:        scrapeEnabled,
		verify:        verify,
		scrapeTimeout: scrapeTimeout,
	}, nil
}

func (p *torrentAlivePlugin) Name() string { return "torrent_alive" }

// scrapeJob is one entry whose seed count must come from a live scrape.
type scrapeJob struct {
	e         *entry.Entry
	hash      string // lowercase hex info hash
	announces []string
	feedSeeds int
	hasFeed   bool
}

// maxConcurrentTrackers bounds how many distinct trackers are scraped in
// parallel in one Process call.
const maxConcurrentTrackers = 8

// Process applies the seed gate to all entries at once. Feed-count decisions
// are immediate; entries that need a live scrape are batched per tracker
// (each request carries up to ~70 info hashes) with distinct trackers
// queried in parallel, so a discover-scale run costs a handful of requests
// instead of one 15-second-timeout scrape per entry.
func (p *torrentAlivePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	var jobs []scrapeJob
	fallback := func(e *entry.Entry, feedSeeds int, hasFeed bool, why string) {
		if hasFeed {
			tc.Logger.Debug("torrent_alive: "+why+" — using feed seed count",
				"entry", e.Title, "seeds", feedSeeds)
			_ = p.applyMinSeeds(e, feedSeeds)
			return
		}
		tc.Logger.Debug("torrent_alive: "+why+" — leaving undecided", "entry", e.Title)
	}

	for _, e := range entries {
		feedSeeds, hasFeed := 0, false
		if v, ok := e.Get(entry.FieldTorrentSeeds); ok {
			feedSeeds, hasFeed = toInt(v), true
		}
		if hasFeed && !p.verify {
			tc.Logger.Debug("torrent_alive: fast path (seeds)", "entry", e.Title, "seeds", feedSeeds)
			_ = p.applyMinSeeds(e, feedSeeds)
			continue
		}
		if !p.scrape {
			fallback(e, feedSeeds, hasFeed, "scrape disabled")
			continue
		}
		if e.GetString(entry.FieldTorrentInfoHash) == "" {
			if err := p.populate(ctx, e); err != nil {
				tc.Logger.Debug("torrent_alive: could not resolve torrent metadata",
					"entry", e.Title, "err", err)
			}
		}
		hash := strings.ToLower(e.GetString(entry.FieldTorrentInfoHash))
		announces := announceList(e)
		if hash == "" || len(announces) == 0 {
			fallback(e, feedSeeds, hasFeed, "no info hash or announces")
			continue
		}
		jobs = append(jobs, scrapeJob{e: e, hash: hash, announces: announces, feedSeeds: feedSeeds, hasFeed: hasFeed})
	}

	if len(jobs) > 0 {
		t0 := time.Now()
		results := p.scrapeAll(ctx, tc, jobs)
		resolved := 0
		for _, j := range jobs {
			if seeds, ok := results[j.hash]; ok {
				resolved++
				j.e.SetTorrentInfo(entry.TorrentInfo{Seeds: seeds})
				_ = p.applyMinSeeds(j.e, seeds)
				continue
			}
			fallback(j.e, j.feedSeeds, j.hasFeed, "scrape failed")
		}
		tc.Logger.Info("torrent_alive: batch scrape",
			"entries", len(jobs), "resolved", resolved,
			"duration", time.Since(t0).Round(time.Millisecond))
	}
	return entry.PassThrough(entries), nil
}

// scrapeAll resolves seed counts for the jobs, returning hash → seeders.
// Rounds walk each job's announce list: round r groups still-unresolved jobs
// by announces[r] and batch-scrapes each tracker, trackers in parallel. Three
// rounds bound the walk — beyond that a torrent's trackers are all dead.
func (p *torrentAlivePlugin) scrapeAll(ctx context.Context, tc *plugin.TaskContext, jobs []scrapeJob) map[string]int {
	results := make(map[string]int, len(jobs))
	var mu sync.Mutex

	const maxRounds = 3
	for round := 0; round < maxRounds; round++ {
		groups := map[string][]string{} // announce URL → hashes
		for _, j := range jobs {
			mu.Lock()
			_, done := results[j.hash]
			mu.Unlock()
			if done || round >= len(j.announces) {
				continue
			}
			groups[j.announces[round]] = append(groups[j.announces[round]], j.hash)
		}
		if len(groups) == 0 {
			break
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, maxConcurrentTrackers)
		for announce, hashes := range groups {
			wg.Add(1)
			go func(announce string, hashes []string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				scrapeCtx, cancel := context.WithTimeout(ctx, p.scrapeTimeout)
				defer cancel()
				t0 := time.Now()
				m, err := tracker.ScrapeBatch(scrapeCtx, hashes, announce)
				host := announce
				if u, uerr := url.Parse(announce); uerr == nil {
					host = u.Host
				}
				if err != nil {
					tc.Logger.Debug("torrent_alive: tracker scrape failed",
						"tracker", host, "hashes", len(hashes),
						"duration", time.Since(t0).Round(time.Millisecond), "err", err)
					return
				}
				// Many private trackers honor only ONE info_hash per scrape
				// request and silently ignore the rest. When a multi-hash
				// batch comes back with at most one answer, treat the tracker
				// as single-hash-only and scrape the unanswered hashes
				// individually — in parallel, so coverage is kept without the
				// old one-at-a-time wall-clock.
				if len(hashes) > 1 && len(m) <= 1 {
					rest := make([]string, 0, len(hashes))
					for _, h := range hashes {
						if _, ok := m[h]; !ok {
							rest = append(rest, h)
						}
					}
					singles := p.scrapeSingles(ctx, announce, rest)
					if m == nil {
						m = singles
					} else {
						for h, s := range singles {
							m[h] = s
						}
					}
					tc.Logger.Debug("torrent_alive: single-hash tracker fallback",
						"tracker", host, "hashes", len(rest), "answered", len(singles))
				}
				tc.Logger.Debug("torrent_alive: tracker scrape ok",
					"tracker", host, "asked", len(hashes), "answered", len(m),
					"duration", time.Since(t0).Round(time.Millisecond))
				mu.Lock()
				for h, s := range m {
					results[h] = s
				}
				mu.Unlock()
			}(announce, hashes)
		}
		wg.Wait()
	}
	return results
}

// maxConcurrentSingles bounds parallel single-hash scrapes against one
// tracker in the single-hash-only fallback. The total request count equals
// the old serial behavior; only the wall-clock changes.
const maxConcurrentSingles = 8

// scrapeSingles scrapes each hash individually against one tracker, in
// parallel, for trackers that ignore extra info_hash parameters.
func (p *torrentAlivePlugin) scrapeSingles(ctx context.Context, announce string, hashes []string) map[string]int {
	out := make(map[string]int, len(hashes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentSingles)
	for _, h := range hashes {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, p.scrapeTimeout)
			defer cancel()
			m, err := tracker.ScrapeBatch(sctx, []string{h}, announce)
			if err != nil {
				return
			}
			if s, ok := m[h]; ok {
				mu.Lock()
				out[h] = s
				mu.Unlock()
			}
		}(h)
	}
	wg.Wait()
	return out
}

// filter applies the gate to a single entry — kept for tests and as the
// simplest description of per-entry semantics; Process is the batched
// implementation used at runtime.
func (p *torrentAlivePlugin) filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	_, err := p.Process(ctx, tc, []*entry.Entry{e})
	return err
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
	if v, ok := e.Get(entry.FieldTorrentAnnounceList); ok {
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
	if u := e.GetString(entry.FieldTorrentAnnounce); u != "" {
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
