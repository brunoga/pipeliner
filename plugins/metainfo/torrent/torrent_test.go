package torrent

import (
	"context"
	"fmt"
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
	p, err := newPlugin(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*torrentPlugin)
}

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test"}
}

// --- tests ---

func TestAnnotateLocalFile(t *testing.T) {
	torrentData := makeTorrent("my.show.s01e01.mkv", 2_000_000_000)
	dir := t.TempDir()
	path := filepath.Join(dir, "my.show.s01e01.torrent")
	if err := os.WriteFile(path, torrentData, 0o644); err != nil {
		t.Fatal(err)
	}

	e := entry.New("my.show.s01e01", "file://"+path)
	e.Set("location", path)

	p := makePlugin(t)
	if err := p.Annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("torrent_name"); v != "my.show.s01e01.mkv" {
		t.Errorf("torrent_name: got %q", v)
	}
	if v := e.GetInt("torrent_size"); v != 2_000_000_000 {
		t.Errorf("torrent_size: got %d", v)
	}
	if v := e.GetString("torrent_info_hash"); len(v) != 40 {
		t.Errorf("torrent_info_hash: got %q (want 40 hex chars)", v)
	}
	if v := e.GetString("torrent_announce"); v != "http://tracker.example/announce" {
		t.Errorf("torrent_announce: got %q", v)
	}
	if v := e.GetString("torrent_comment"); v != "unit test torrent" {
		t.Errorf("torrent_comment: got %q", v)
	}
	if v := e.GetString("torrent_created_by"); v != "pipeliner-test" {
		t.Errorf("torrent_created_by: got %q", v)
	}
	if v := e.GetInt("torrent_creation_date"); v != 1700000000 {
		t.Errorf("torrent_creation_date: got %d", v)
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
	if err := p.Annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("torrent_name"); v != "remote.mkv" {
		t.Errorf("torrent_name: got %q", v)
	}
	if v := e.GetInt("torrent_size"); v != 500_000_000 {
		t.Errorf("torrent_size: got %d", v)
	}
}

func TestAnnotateNonTorrentEntry(t *testing.T) {
	e := entry.New("article", "http://example.com/news/article.html")

	p := makePlugin(t)
	if err := p.Annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	// No torrent fields should be set
	if v := e.GetString("torrent_name"); v != "" {
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
	err := p.Annotate(context.Background(), tc(), e)
	if err == nil {
		t.Error("expected error for HTTP 404")
	}
}

func TestPluginRegistered(t *testing.T) {
	if _, ok := plugin.Lookup("metainfo_torrent"); !ok {
		t.Error("metainfo_torrent not registered")
	}
}
