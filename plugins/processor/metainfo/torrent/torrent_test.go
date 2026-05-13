package torrent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// --- helpers ---

func bs(s string) string { return fmt.Sprintf("%d:%s", len(s), s) }
func bi(n int64) string  { return fmt.Sprintf("i%de", n) }

func makeTorrent(name string, size int64) []byte {
	pieces := string(make([]byte, 20))
	raw := "d" +
		bs("announce") + bs("http://tracker.example/announce") +
		bs("comment") + bs("unit test torrent") +
		bs("created by") + bs("pipeliner-test") +
		bs("creation date") + bi(1700000000) +
		bs("info") + "d" +
		bs("length") + bi(size) +
		bs("name") + bs(name) +
		bs("piece length") + bi(262144) +
		bs("pieces") + bs(pieces) +
		"e" +
		"e"
	return []byte(raw)
}

func makePlugin(t *testing.T) *torrentPlugin {
	t.Helper()
	p, err := newPlugin(map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*torrentPlugin)
}

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

// --- tests ---

func TestAnnotateLocalFile(t *testing.T) {
	torrentData := makeTorrent("my.show.s01e01.mkv", 2_000_000_000)
	dir := t.TempDir()
	path := filepath.Join(dir, "my.show.s01e01.torrent")
	if err := os.WriteFile(path, torrentData, 0o600); err != nil {
		t.Fatal(err)
	}

	e := entry.New("my.show.s01e01", "file://"+path)
	e.Set("file_location", path)

	p := makePlugin(t)
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("title"); v != "my.show.s01e01.mkv" {
		t.Errorf("torrent_name: got %q", v)
	}
	if v := e.GetInt("torrent_file_size"); v != 2_000_000_000 {
		t.Errorf("torrent_size: got %d", v)
	}
	if v := e.GetString("torrent_info_hash"); len(v) != 40 {
		t.Errorf("torrent_info_hash: got %q (want 40 hex chars)", v)
	}
	if v := e.GetString("torrent_announce"); v != "http://tracker.example/announce" {
		t.Errorf("torrent_announce: got %q", v)
	}
	if v := e.GetString("description"); v != "unit test torrent" {
		t.Errorf("torrent_comment: got %q", v)
	}
	if v := e.GetString("torrent_created_by"); v != "pipeliner-test" {
		t.Errorf("torrent_created_by: got %q", v)
	}
	if v := e.GetTime("torrent_creation_date"); v.Unix() != 1700000000 {
		t.Errorf("creation_date: got %v (unix %d)", v, v.Unix())
	}
	if v := e.GetInt("torrent_file_count"); v != 1 {
		t.Errorf("torrent_file_count: got %d", v)
	}
}

func TestAnnotateRemoteURL(t *testing.T) {
	torrentData := makeTorrent("remote.mkv", 500_000_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		w.Write(torrentData) //nolint:errcheck
	}))
	defer srv.Close()

	e := entry.New("remote", srv.URL+"/remote.torrent")

	p := makePlugin(t)
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("title"); v != "remote.mkv" {
		t.Errorf("torrent_name: got %q", v)
	}
	if v := e.GetInt("torrent_file_size"); v != 500_000_000 {
		t.Errorf("torrent_size: got %d", v)
	}
}

func TestAnnotateNonTorrentEntry(t *testing.T) {
	e := entry.New("article", "http://example.com/news/article.html")

	p := makePlugin(t)
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	// No torrent fields should be set
	if v := e.GetString("title"); v != "" {
		t.Errorf("expected no torrent_name for non-torrent entry, got %q", v)
	}
}

func TestAnnotateHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	e := entry.New("bad", srv.URL+"/missing.torrent")

	p := makePlugin(t)
	err := p.annotate(context.Background(), tc(), e)
	if err == nil {
		t.Error("expected error for HTTP 404")
	}
}

// TestAnnotateEnclosureType verifies that an entry whose URL has no .torrent
// suffix is still fetched when rss_enclosure_type signals a torrent file.
func TestAnnotateEnclosureType(t *testing.T) {
	torrentData := makeTorrent("show.s01e01.mkv", 1_000_000_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		w.Write(torrentData) //nolint:errcheck
	}))
	defer srv.Close()

	e := entry.New("show", srv.URL+"/download?id=12345") // no .torrent suffix
	e.Set(entry.FieldRSSEnclosureType, "application/x-bittorrent")

	p := makePlugin(t)
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("title"); v != "show.s01e01.mkv" {
		t.Errorf("expected torrent name, got %q", v)
	}
}

// TestAnnotateTorrentLinkType verifies that an entry with torrent_link_type="torrent"
// is fetched even when the URL has no .torrent suffix (e.g. a Jackett proxy URL).
func TestAnnotateTorrentLinkType(t *testing.T) {
	torrentData := makeTorrent("movie.2025.mkv", 5_000_000_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		w.Write(torrentData) //nolint:errcheck
	}))
	defer srv.Close()

	e := entry.New("movie", srv.URL+"/dl/indexer/?jackett_apikey=abc&path=xyz&file=movie")
	e.Set(entry.FieldTorrentLinkType, "torrent")

	p := makePlugin(t)
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("title"); v != "movie.2025.mkv" {
		t.Errorf("expected torrent name from .torrent bytes, got %q", v)
	}
}

// TestAnnotateMagnetLinkTypeSkipped verifies that entries with
// torrent_link_type="magnet" are skipped by metainfo_torrent.
func TestAnnotateMagnetLinkTypeSkipped(t *testing.T) {
	fetched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetched = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := entry.New("movie", srv.URL+"/dl/indexer/?jackett_apikey=abc&path=xyz&file=movie")
	e.Set(entry.FieldTorrentLinkType, "magnet")

	p := makePlugin(t)
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if fetched {
		t.Error("should not fetch a URL when torrent_link_type=magnet")
	}
}

// TestAnnotateNoFetchWithoutSignal verifies that a plain HTTP URL with no
// torrent signals is not fetched.
func TestAnnotateNoFetchWithoutSignal(t *testing.T) {
	fetched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetched = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := entry.New("page", srv.URL+"/some/page")

	p := makePlugin(t)
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if fetched {
		t.Error("should not fetch a plain HTTP URL with no torrent signals")
	}
}

func TestPluginRegistered(t *testing.T) {
	if _, ok := plugin.Lookup("metainfo_torrent"); !ok {
		t.Error("metainfo_torrent not registered")
	}
}
