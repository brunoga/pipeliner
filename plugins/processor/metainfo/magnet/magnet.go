// Package magnet provides a metainfo plugin that annotates entries with
// metadata extracted from magnet URIs, optionally resolving full torrent
// metadata via DHT using github.com/anacrolix/torrent.
//
// Fields set on the entry from the magnet URI itself:
//
//	torrent_info_hash     - hex SHA-1 info hash (40 chars)
//	torrent_announce      - first tracker announce URL, if any
//	torrent_announce_list - []string of all tracker announce URLs
//	torrent_display_name  - human-readable name from dn= parameter, if present
//
// Fields set after DHT resolution (when the client successfully contacts peers):
//
//	torrent_name       - name from the info dict
//	torrent_size       - total size in bytes (int64)
//	torrent_file_count - number of files (int)
//	torrent_files      - []string of file paths relative to the torrent root
//
// DHT resolution is the slowest stage in most pipelines (tens of seconds for
// a batch), so results are cached by info hash. An info hash cryptographically
// commits to exactly this metadata, so a cached entry can never be wrong — the
// TTL only bounds how long unused entries linger. Keying by info hash rather
// than URL means the same torrent shares one entry across magnet URIs that
// differ only in trackers or display name.
package magnet

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	imagnet "github.com/brunoga/pipeliner/internal/magnet"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_magnet",
		Description: "annotate entries whose URL is a magnet link with info hash, tracker and DHT metadata",
		Role:        plugin.RoleProcessor,
		MayProduce: []string{
			entry.FieldTitle,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentAnnounce,
			entry.FieldTorrentAnnounceList,
			entry.FieldTorrentFileSize,
			entry.FieldTorrentFileCount,
			entry.FieldTorrentFiles,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "resolve_timeout", Type: plugin.FieldTypeDuration, Default: "30s", Hint: "Max time to wait for DHT metadata"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "720h", Hint: "How long resolved DHT metadata is cached by info hash (content is immutable; the TTL only expires unused entries)"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_metainfo_magnet", Display: "Magnet Metadata Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptDuration(cfg, "resolve_timeout", "metainfo_magnet"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "cache_ttl", "metainfo_magnet"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "metainfo_magnet", "resolve_timeout", "cache_ttl")...)
	return errs
}

// magnetInfo is the cacheable result of a DHT resolution — exactly what
// applyInfo writes onto an entry.
type magnetInfo struct {
	Name      string   `json:"name"`
	Size      int64    `json:"size"`
	FileCount int      `json:"file_count"`
	Files     []string `json:"files"`
}

type magnetPlugin struct {
	client         *torrent.Client
	resolveTimeout time.Duration
	// cache maps info hash → resolved metadata. Nil when no store is wired.
	cache *cache.Cache[*magnetInfo]
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	resolveTimeout := 30 * time.Second
	if v, ok := cfg["resolve_timeout"]; ok {
		s, _ := v.(string)
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, err
		}
		resolveTimeout = d
	}

	tcfg := torrent.NewDefaultClientConfig()
	tcfg.NoUpload = true
	tcfg.Seed = false
	tcfg.ListenPort = 0
	tcfg.DataDir = os.TempDir()

	cl, err := torrent.NewClient(tcfg)
	if err != nil {
		return nil, err
	}

	var infoCache *cache.Cache[*magnetInfo]
	if db != nil {
		ttl := 720 * time.Hour
		if v, _ := cfg["cache_ttl"].(string); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				cl.Close()
				return nil, fmt.Errorf("metainfo_magnet: invalid cache_ttl %q: %w", v, err)
			}
			ttl = d
		}
		infoCache = cache.NewPersistent[*magnetInfo](ttl, db.Bucket("cache_metainfo_magnet"))
		infoCache.Preload()
	}

	return &magnetPlugin{
		client:         cl,
		resolveTimeout: resolveTimeout,
		cache:          infoCache,
	}, nil
}

func (p *magnetPlugin) Name() string { return "metainfo_magnet" }

// Shutdown closes the underlying DHT client, releasing its goroutines and
// sockets. Called at process exit (daemon) or after the run completes.
func (p *magnetPlugin) Shutdown() { p.client.Close() }

// isMagnetEntry reports whether this entry should be handled as a magnet link.
// It checks torrent_link_type first (set by upstream sources such as Jackett),
// then falls back to inspecting the URL prefix.
func isMagnetEntry(e *entry.Entry) bool {
	switch e.GetString(entry.FieldTorrentLinkType) {
	case "magnet":
		return true
	case "torrent":
		return false
	}
	return strings.HasPrefix(e.URL, "magnet:")
}

// annotateBatch first annotates all entries
// from their magnet URIs, then fires DHT resolution for all of them in
// parallel, waiting up to resolveTimeout for each.
func (p *magnetPlugin) annotateBatch(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	log := tc.Logger
	log.Debug("metainfo_magnet: batch received", "entries", len(entries), "resolve_timeout", p.resolveTimeout)

	type work struct {
		t *torrent.Torrent
		e *entry.Entry
	}

	var jobs []work
	cached := 0
	for _, e := range entries {
		if !isMagnetEntry(e) {
			log.Debug("metainfo_magnet: skipping entry — not a magnet", "entry", e.URL)
			continue
		}
		if err := annotateFromURI(e); err != nil {
			log.Error("metainfo_magnet: failed to parse magnet URI", "entry", e.URL, "err", err)
			continue
		}
		// A cache hit skips the DHT client entirely — that is where the time
		// is saved, since resolution dominates the run.
		if hash := e.GetString(entry.FieldTorrentInfoHash); hash != "" {
			if mi, ok := p.cache.Get(hash); ok && mi != nil {
				applyCachedInfo(mi, e)
				cached++
				log.Debug("metainfo_magnet: cache hit", "entry", e.URL, "info_hash", hash)
				continue
			}
		}
		log.Debug("metainfo_magnet: URI parsed",
			"entry", e.URL,
			"info_hash", e.GetString(entry.FieldTorrentInfoHash),
			"trackers", announceCount(e),
			"announce", e.GetString(entry.FieldTorrentAnnounce),
			"display_name", e.GetString(entry.FieldTitle),
		)
		t, err := p.client.AddMagnet(e.URL)
		if err != nil {
			log.Error("metainfo_magnet: failed to add magnet to DHT client", "entry", e.URL, "err", err)
			continue
		}
		jobs = append(jobs, work{t: t, e: e})
	}

	log.Debug("metainfo_magnet: DHT resolution queued",
		"count", len(jobs), "from_cache", cached, "timeout", p.resolveTimeout)

	if len(jobs) == 0 {
		if cached > 0 {
			log.Info("metainfo_magnet: all metadata served from cache", "entries", cached)
		}
		return nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, p.resolveTimeout)
	defer cancel()

	var (
		wg       sync.WaitGroup
		resolved atomic.Int32
		timedOut atomic.Int32
	)
	for _, j := range jobs {
		wg.Add(1)
		go func(t *torrent.Torrent, e *entry.Entry) {
			defer wg.Done()
			defer t.Drop()
			select {
			case <-t.GotInfo():
				mi := applyInfo(t, e)
				if hash := e.GetString(entry.FieldTorrentInfoHash); hash != "" {
					p.cache.Set(hash, mi)
				}
				resolved.Add(1)
				size, _ := e.Get(entry.FieldTorrentFileSize)
				log.Debug("metainfo_magnet: DHT resolved",
					"entry", e.URL,
					"name", e.GetString(entry.FieldTitle),
					"size", size,
					"files", e.GetInt(entry.FieldTorrentFileCount),
				)
			case <-resolveCtx.Done():
				timedOut.Add(1)
				log.Debug("metainfo_magnet: DHT timed out",
					"entry", e.URL,
					"timeout", p.resolveTimeout,
				)
			}
		}(j.t, j.e)
	}
	wg.Wait()

	log.Debug("metainfo_magnet: batch complete",
		"queued", len(jobs),
		"from_cache", cached,
		"resolved", resolved.Load(),
		"timed_out", timedOut.Load(),
	)
	return nil
}

// Process implements ProcessorPlugin. It delegates to AnnotateBatch so all
// DHT resolutions in the batch run in parallel under a shared timeout.
func (p *magnetPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	live := make([]*entry.Entry, 0, len(entries))
	for _, e := range entries {
		if !e.IsRejected() && !e.IsFailed() {
			live = append(live, e)
		}
	}
	if err := p.annotateBatch(ctx, tc, live); err != nil {
		tc.Logger.Warn("metainfo_magnet error", "err", err)
	}
	return entries, nil
}

// annotateFromURI parses the magnet URI and sets torrent_info_hash,
// torrent_announce, torrent_announce_list, and torrent_display_name.
func annotateFromURI(e *entry.Entry) error {
	if !strings.HasPrefix(e.URL, "magnet:") {
		return nil
	}
	m, err := imagnet.Parse(e.URL)
	if err != nil {
		return fmt.Errorf("malformed magnet URI: %w", err)
	}

	ti := entry.TorrentInfo{
		InfoHash:     m.InfoHash,
		AnnounceList: m.Trackers,
	}
	if len(m.Trackers) > 0 {
		ti.Announce = m.Trackers[0]
	}
	// Magnet display name is an inferred title; only adopt it if no canonical
	// title is already set on the entry.
	e.SetTitleIfEmpty(m.DisplayName)
	e.SetTorrentInfo(ti)
	return nil
}

// announceCount returns the number of tracker URLs stored in the entry's
// torrent_announce_list field, or 0 if unset.
func announceCount(e *entry.Entry) int {
	v, ok := e.Get(entry.FieldTorrentAnnounceList)
	if !ok {
		return 0
	}
	if list, ok := v.([]string); ok {
		return len(list)
	}
	return 0
}

// applyInfo copies metadata from the resolved torrent info into the entry.
func applyInfo(t *torrent.Torrent, e *entry.Entry) *magnetInfo {
	files := t.Files()
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path()
	}
	mi := &magnetInfo{Name: t.Name(), Size: t.Length(), FileCount: len(files), Files: paths}
	applyCachedInfo(mi, e)
	return mi
}

// applyCachedInfo writes resolved metadata onto an entry. Shared by the live
// DHT path and the cache path so both produce identical entries.
func applyCachedInfo(mi *magnetInfo, e *entry.Entry) {
	// Resolved torrent name is the release filename — inferred, not canonical.
	e.SetTitleIfEmpty(mi.Name)
	e.SetTorrentInfo(entry.TorrentInfo{
		FileSize:  mi.Size,
		FileCount: mi.FileCount,
		Files:     mi.Files,
	})
}
