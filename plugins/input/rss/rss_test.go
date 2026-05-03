package rss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test"}
}

func serveFile(t *testing.T, path string) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write(data) //nolint:errcheck
	}))
}

func TestRSS2Parse(t *testing.T) {
	srv := serveFile(t, "testdata/rss2.xml")
	defer srv.Close()

	p, err := newPlugin(map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := p.(*rssPlugin).Run(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Title != "Episode One" {
		t.Errorf("entry 0 title: got %q", entries[0].Title)
	}
	if entries[0].URL != "http://example.com/ep1" {
		t.Errorf("entry 0 URL: got %q", entries[0].URL)
	}
	if v := entries[0].GetString("rss_pubdate"); v == "" {
		t.Error("rss_pubdate should be set")
	}
	if v := entries[0].GetString("rss_feed"); v != srv.URL {
		t.Errorf("rss_feed: got %q, want %q", v, srv.URL)
	}
}

func TestRSS2EnclosureURL(t *testing.T) {
	srv := serveFile(t, "testdata/rss2.xml")
	defer srv.Close()

	p, _ := newPlugin(map[string]any{"url": srv.URL})
	entries, _ := p.(*rssPlugin).Run(context.Background(), makeCtx())

	// Third item has an enclosure — URL should be the enclosure URL.
	torrentEntry := entries[2]
	if torrentEntry.URL != "http://example.com/file.torrent" {
		t.Errorf("enclosure URL: got %q, want torrent URL", torrentEntry.URL)
	}
	// Original page link preserved in rss_link.
	if v := torrentEntry.GetString("rss_link"); v != "http://example.com/torrent-page" {
		t.Errorf("rss_link: got %q", v)
	}
}

func TestAtomParse(t *testing.T) {
	srv := serveFile(t, "testdata/atom.xml")
	defer srv.Close()

	p, err := newPlugin(map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := p.(*rssPlugin).Run(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Title != "Atom Entry One" {
		t.Errorf("entry 0 title: got %q", entries[0].Title)
	}
	if entries[0].URL != "http://example.com/atom-ep1" {
		t.Errorf("entry 0 URL: got %q", entries[0].URL)
	}
}

func TestAtomEnclosurePreferred(t *testing.T) {
	srv := serveFile(t, "testdata/atom.xml")
	defer srv.Close()

	p, _ := newPlugin(map[string]any{"url": srv.URL})
	entries, _ := p.(*rssPlugin).Run(context.Background(), makeCtx())

	enc := entries[2]
	if enc.URL != "http://example.com/atom.torrent" {
		t.Errorf("atom enclosure URL: got %q", enc.URL)
	}
}

func TestHTTPErrorNonRetriable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, _ := newPlugin(map[string]any{"url": srv.URL})
	_, err := p.(*rssPlugin).Run(context.Background(), makeCtx())
	if err == nil {
		t.Error("expected error on 404")
	}
}

func TestHTTPError5xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p, _ := newPlugin(map[string]any{"url": srv.URL})
	rp := p.(*rssPlugin)
	// Speed up retries for the test.
	_, err := rp.Run(context.Background(), makeCtx())
	if err == nil {
		t.Error("expected error after retries")
	}
	if attempts < 2 {
		t.Errorf("expected at least 2 attempts (retry), got %d", attempts)
	}
}

func TestMissingURL(t *testing.T) {
	_, err := newPlugin(map[string]any{})
	if err == nil {
		t.Error("expected error when url is missing")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("rss")
	if !ok {
		t.Fatal("rss plugin not registered")
	}
	if d.PluginPhase != plugin.PhaseInput {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}
