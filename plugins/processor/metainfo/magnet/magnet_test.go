package magnet

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const (
	hexHash = "aabbccddeeff00112233445566778899aabbccdd"
	tracker = "http://tracker.example.com/announce"
)

func taskCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Logger: slog.Default()}
}

func annotate(t *testing.T, e *entry.Entry) {
	t.Helper()
	if err := annotateFromURI(e); err != nil {
		t.Fatalf("Annotate: %v", err)
	}
}

func TestAnnotatesMagnetURL(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash + "&tr=" + tracker
	e := entry.New("some title", uri)
	annotate(t, e)

	if v := e.GetString("torrent_info_hash"); v != hexHash {
		t.Errorf("info_hash: got %q, want %q", v, hexHash)
	}
	if v := e.GetString("torrent_announce"); v != tracker {
		t.Errorf("announce: got %q, want %q", v, tracker)
	}
}

func TestAnnotatesAnnounceList(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash +
		"&tr=http://t1.example.com/announce" +
		"&tr=udp://t2.example.com:6969"
	e := entry.New("title", uri)
	annotate(t, e)

	v, ok := e.Get("torrent_announce_list")
	if !ok {
		t.Fatal("announce_list not set")
	}
	list, ok := v.([]string)
	if !ok {
		t.Fatalf("announce_list type: got %T", v)
	}
	if len(list) != 2 {
		t.Errorf("want 2 trackers, got %d", len(list))
	}
}

func TestAnnotatesDisplayName(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash + "&dn=My+Show+S01E01"
	e := entry.New("title", uri)
	annotate(t, e)

	if v := e.GetString("title"); v == "" {
		t.Error("title should be set")
	}
}

func TestSkipsNonMagnetURL(t *testing.T) {
	e := entry.New("title", "http://example.com/file.torrent")
	annotate(t, e)

	if _, ok := e.Get("torrent_info_hash"); ok {
		t.Error("info_hash should not be set for non-magnet URL")
	}
}

func TestSkipsMalformedMagnet(t *testing.T) {
	e := entry.New("title", "magnet:?xt=urn:btih:BADSHORTEST")
	err := annotateFromURI(e)
	if err == nil {
		t.Error("expected error for malformed magnet URI")
	}
	if _, ok := e.Get("torrent_info_hash"); ok {
		t.Error("info_hash should not be set for malformed magnet")
	}
}

func TestNoTrackersNoAnnounceField(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash
	e := entry.New("title", uri)
	annotate(t, e)

	if _, ok := e.Get("torrent_announce"); ok {
		t.Error("announce should not be set when no trackers")
	}
}

// TestAnnotateBatchTorrentLinkTypeMagnet verifies that AnnotateBatch processes
// an entry with torrent_link_type="magnet" even without the magnet: URL prefix
// (the Jackett parser sets the URL to the actual magnet URI, but this tests
// the field check directly).
func TestAnnotateBatchTorrentLinkTypeMagnet(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash + "&tr=" + tracker
	e := entry.New("title", uri)
	e.Set(entry.FieldTorrentLinkType, "magnet")

	p, err := newPlugin(map[string]any{"resolve_timeout": "1ms"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	mp := p.(*magnetPlugin)
	if err := mp.annotateBatch(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("AnnotateBatch: %v", err)
	}
	if v := e.GetString("torrent_info_hash"); v != hexHash {
		t.Errorf("info_hash: got %q, want %q", v, hexHash)
	}
}

// TestAnnotateBatchTorrentLinkTypeTorrentSkipped verifies that AnnotateBatch
// skips entries with torrent_link_type="torrent" even if the URL happens to
// look like a magnet URI (should not happen in practice, but tests field priority).
func TestAnnotateBatchTorrentLinkTypeTorrentSkipped(t *testing.T) {
	e := entry.New("title", "magnet:?xt=urn:btih:"+hexHash)
	e.Set(entry.FieldTorrentLinkType, "torrent") // override: engine says it's a torrent

	p := &magnetPlugin{}
	if err := p.annotateBatch(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("AnnotateBatch: %v", err)
	}
	if _, ok := e.Get("torrent_info_hash"); ok {
		t.Error("torrent entry should not be processed by metainfo_magnet")
	}
}

// TestAnnotateBatchSkipsNonMagnet verifies that AnnotateBatch ignores entries
// whose URL is not a magnet URI and does not block.
func TestAnnotateBatchSkipsNonMagnet(t *testing.T) {
	p := &magnetPlugin{} // no client — should not be reached for non-magnet entries
	entries := []*entry.Entry{
		entry.New("torrent", "http://example.com/file.torrent"),
		entry.New("page", "https://example.com/"),
	}
	if err := p.annotateBatch(context.Background(), taskCtx(), entries); err != nil {
		t.Fatalf("AnnotateBatch: %v", err)
	}
	for _, e := range entries {
		if _, ok := e.Get("torrent_info_hash"); ok {
			t.Errorf("%s: info_hash should not be set", e.URL)
		}
	}
}

// TestAnnotateBatchSetsURIFields verifies that AnnotateBatch sets URI-derived
// fields even when DHT resolution times out immediately.
func TestAnnotateBatchSetsURIFields(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash + "&tr=" + tracker + "&dn=My+Show"

	p, err := newPlugin(map[string]any{"resolve_timeout": "1ms"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	mp := p.(*magnetPlugin)

	e := entry.New("title", uri)
	if err := mp.annotateBatch(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("AnnotateBatch: %v", err)
	}

	if v := e.GetString("torrent_info_hash"); v != hexHash {
		t.Errorf("info_hash: got %q, want %q", v, hexHash)
	}
	if v := e.GetString("torrent_announce"); v != tracker {
		t.Errorf("announce: got %q, want %q", v, tracker)
	}
	if v := e.GetString("title"); v == "" {
		t.Error("title should be set")
	}
}

// TestAnnotateBatchMalformedMagnetSkipped verifies that a malformed magnet URI
// in a batch does not cause an error and leaves the entry unmodified.
func TestAnnotateBatchMalformedMagnetSkipped(t *testing.T) {
	p, err := newPlugin(map[string]any{"resolve_timeout": "1ms"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	mp := p.(*magnetPlugin)

	e := entry.New("title", "magnet:?xt=urn:btih:TOOSHORT")
	if err := mp.annotateBatch(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("AnnotateBatch: %v", err)
	}
	if _, ok := e.Get("torrent_info_hash"); ok {
		t.Error("info_hash should not be set for malformed magnet")
	}
}

// TestNewPluginInvalidTimeout verifies that an invalid resolve_timeout returns an error.
func TestNewPluginInvalidTimeout(t *testing.T) {
	_, err := newPlugin(map[string]any{"resolve_timeout": "not-a-duration"}, nil)
	if err == nil {
		t.Error("expected error for invalid resolve_timeout")
	}
}

// TestCacheHitSkipsDHT is the point of the cache: DHT resolution dominates
// the run (15s of a 23s on-demand pipeline), so a previously-resolved info
// hash must be served from the store without ever touching the DHT client.
// The 1ms resolve timeout makes the difference unmistakable — a cache miss
// cannot possibly populate these fields in that budget.
func TestCacheHitSkipsDHT(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	p, err := newPlugin(map[string]any{"resolve_timeout": "1ms"}, db)
	if err != nil {
		t.Fatal(err)
	}
	mp := p.(*magnetPlugin)
	t.Cleanup(mp.Shutdown)

	// Seed the cache as a previous successful resolution would have.
	mp.cache.Set(hexHash, &magnetInfo{
		Name: "Some.Release.2026.1080p", Size: 4_000_000_000,
		FileCount: 2, Files: []string{"movie.mkv", "subs/en.srt"},
	})

	e := entry.New("", "magnet:?xt=urn:btih:"+hexHash+"&tr="+tracker)
	if err := mp.annotateBatch(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if got := e.GetInt(entry.FieldTorrentFileCount); got != 2 {
		t.Errorf("file_count = %d, want 2 (served from cache)", got)
	}
	if got, _ := e.Get(entry.FieldTorrentFileSize); got != int64(4_000_000_000) {
		t.Errorf("file_size = %v, want 4000000000", got)
	}
	files, _ := e.Get(entry.FieldTorrentFiles)
	if paths, ok := files.([]string); !ok || len(paths) != 2 || paths[0] != "movie.mkv" {
		t.Errorf("files = %v, want the cached paths", files)
	}
	if got := e.GetString(entry.FieldTitle); got != "Some.Release.2026.1080p" {
		t.Errorf("title field = %q, want the cached name", got)
	}
}

// A cache miss must leave the entry unresolved rather than inventing data,
// and must not blow up when no store is wired (nil cache).
func TestCacheMissAndNilCacheAreSafe(t *testing.T) {
	for _, tc := range []struct {
		name string
		db   *store.SQLiteStore
	}{{"nil cache", nil}, {"empty cache", mustStore(t)}} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := newPlugin(map[string]any{"resolve_timeout": "1ms"}, tc.db)
			if err != nil {
				t.Fatal(err)
			}
			mp := p.(*magnetPlugin)
			t.Cleanup(mp.Shutdown)
			e := entry.New("", "magnet:?xt=urn:btih:"+hexHash)
			if err := mp.annotateBatch(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
				t.Fatal(err)
			}
			// Info hash comes from the URI regardless; file data does not.
			if e.GetString(entry.FieldTorrentInfoHash) != hexHash {
				t.Error("info hash should still come from the URI")
			}
			if e.GetInt(entry.FieldTorrentFileCount) != 0 {
				t.Error("unresolved entry must not carry file data")
			}
		})
	}
}

func mustStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCacheTTLValidation(t *testing.T) {
	if errs := validate(map[string]any{"cache_ttl": "24h"}); len(errs) != 0 {
		t.Errorf("valid cache_ttl: %v", errs)
	}
	if errs := validate(map[string]any{"cache_ttl": "nonsense"}); len(errs) == 0 {
		t.Error("invalid cache_ttl must fail validation")
	}
}

// A config with more than one metainfo_magnet node builds more than one
// torrent client, and they must not share a scratch directory. They used to:
// DataDir was os.TempDir() for every one of them, so each opened the same
// piece-completion database at <tmp>/.torrent.bolt.db, the first won the lock
// and the rest logged
//
//	couldn't open piece completion db in "/tmp": timeout
//
// at every daemon start. Three nodes in a live config meant two warnings each
// time, and a stray bolt file nobody owned.
func TestClientsDoNotShareAScratchDirectory(t *testing.T) {
	shared := filepath.Join(os.TempDir(), ".torrent.bolt.db")
	if _, err := os.Stat(shared); err == nil {
		t.Skipf("%s already exists; cannot attribute it", shared)
	}

	seen := map[string]bool{}
	var plugins []*magnetPlugin
	for i := 0; i < 3; i++ {
		pl, err := newPlugin(map[string]any{}, nil)
		if err != nil {
			t.Skipf("cannot start a torrent client here: %v", err)
		}
		p := pl.(*magnetPlugin)
		plugins = append(plugins, p)

		if p.dataDir == "" || p.dataDir == os.TempDir() {
			t.Errorf("client %d uses the shared temp root: %q", i, p.dataDir)
		}
		if seen[p.dataDir] {
			t.Errorf("client %d reuses scratch directory %s", i, p.dataDir)
		}
		seen[p.dataDir] = true
		if _, err := os.Stat(p.dataDir); err != nil {
			t.Errorf("client %d: scratch directory missing: %v", i, err)
		}
	}

	if _, err := os.Stat(shared); err == nil {
		t.Errorf("a piece-completion db was created at %s; the clients are contending for it", shared)
	}

	// Shutdown takes its scratch directory with it, so a long-running daemon
	// does not leave one behind per reload.
	for _, p := range plugins {
		dir := p.dataDir
		p.Shutdown()
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("scratch directory survived shutdown: %s", dir)
		}
	}
}
