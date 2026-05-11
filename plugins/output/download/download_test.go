package download

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body)) //nolint:errcheck
	}))
}

func TestDownloadsFile(t *testing.T) {
	srv := serve(t, "file content")
	defer srv.Close()

	dir := t.TempDir()
	p, err := newPlugin(map[string]any{"path": dir}, nil)
	if err != nil {
		t.Fatal(err)
	}

	e := entry.New("Test File", srv.URL+"/file.torrent")
	err = p.(*downloadPlugin).Output(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "file.torrent")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if string(data) != "file content" {
		t.Errorf("content: got %q", data)
	}
}

func TestDownloadPathFieldSet(t *testing.T) {
	srv := serve(t, "x")
	defer srv.Close()

	dir := t.TempDir()
	p, _ := newPlugin(map[string]any{"path": dir}, nil)
	e := entry.New("Test", srv.URL+"/ep.mkv")
	p.(*downloadPlugin).Output(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	if v := e.GetString("download_path"); v == "" {
		t.Error("download_path should be set after download")
	}
}

func TestCustomFilenameTemplate(t *testing.T) {
	srv := serve(t, "data")
	defer srv.Close()

	dir := t.TempDir()
	p, err := newPlugin(map[string]any{
		"path":     dir,
		"filename": "{{.series_name}}.S{{.series_season}}E01.mkv",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("Test", srv.URL+"/anything")
	e.Set("series_name", "My Show")
	e.Set("series_season", 2)
	p.(*downloadPlugin).Output(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	dest := filepath.Join(dir, "My Show.S2E01.mkv")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected file %s: %v", dest, err)
	}
}

func TestAtomicWrite(t *testing.T) {
	srv := serve(t, "content")
	defer srv.Close()

	dir := t.TempDir()
	p, _ := newPlugin(map[string]any{"path": dir}, nil)
	e := entry.New("T", srv.URL+"/f.bin")
	p.(*downloadPlugin).Output(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	// .part file must not remain after success.
	entries, _ := os.ReadDir(dir)
	for _, de := range entries {
		if filepath.Ext(de.Name()) == ".part" {
			t.Errorf("stale .part file: %s", de.Name())
		}
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	dir := t.TempDir()
	p, _ := newPlugin(map[string]any{"path": dir}, nil)
	e := entry.New("T", srv.URL+"/f.bin")
	// Error is logged per-entry; Output itself succeeds.
	err := p.(*downloadPlugin).Output(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Errorf("Output should not return error for per-entry failures: %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	// Slow server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until client disconnects.
		<-r.Context().Done()
	}))
	defer srv.Close()

	dir := t.TempDir()
	p, _ := newPlugin(map[string]any{"path": dir}, nil)
	e := entry.New("T", srv.URL+"/f.bin")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	p.(*downloadPlugin).Output(ctx, makeCtx(), []*entry.Entry{e}) //nolint:errcheck
	// No .part file should linger.
	entries, _ := os.ReadDir(dir)
	for _, de := range entries {
		if filepath.Ext(de.Name()) == ".part" {
			t.Errorf("stale .part file after cancel: %s", de.Name())
		}
	}
}

func TestMissingPath(t *testing.T) {
	if _, err := newPlugin(map[string]any{}, nil); err == nil {
		t.Error("expected error when path missing")
	}
}

func TestURLBasename(t *testing.T) {
	cases := []struct{ url, want string }{
		{"http://x.com/file.torrent", "file.torrent"},
		{"http://x.com/path/to/ep.mkv", "ep.mkv"},
		{"http://x.com/", "download"},
		{"not-a-url", "download"},
	}
	for _, tc := range cases {
		got := urlBasename(tc.url)
		if got != tc.want {
			t.Errorf("urlBasename(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("download")
	if !ok {
		t.Fatal("download plugin not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("phase: got %v", d.Role)
	}
}
