package jackett

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// torznabResponse builds a minimal Torznab RSS document from the given items.
func torznabResponse(items []torznabItem) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString(`<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">`)
	sb.WriteString(`<channel>`)
	for _, it := range items {
		fmt.Fprintf(&sb, `<item><title>%s</title>`, it.Title)
		if it.Enclosure.URL != "" {
			fmt.Fprintf(&sb, `<enclosure url="%s" type="application/x-bittorrent"/>`, it.Enclosure.URL)
		} else if it.Link != "" {
			fmt.Fprintf(&sb, `<link>%s</link>`, it.Link)
		}
		if it.Size > 0 {
			fmt.Fprintf(&sb, `<size>%d</size>`, it.Size)
		}
		for _, a := range it.Attrs {
			fmt.Fprintf(&sb, `<torznab:attr name="%s" value="%s"/>`, a.Name, a.Value)
		}
		sb.WriteString(`</item>`)
	}
	sb.WriteString(`</channel></rss>`)
	return sb.String()
}

func makePlugin(t *testing.T, baseURL, apiKey string, extra map[string]any) *jackettPlugin {
	t.Helper()
	cfg := map[string]any{"url": baseURL, "api_key": apiKey}
	maps.Copy(cfg, extra)
	p, err := newPlugin(cfg)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*jackettPlugin)
}

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// --- constructor tests ---

func TestMissingURL(t *testing.T) {
	_, err := newPlugin(map[string]any{"api_key": "key"})
	if err == nil {
		t.Error("expected error when url is missing")
	}
}

func TestMissingAPIKey(t *testing.T) {
	_, err := newPlugin(map[string]any{"url": "http://localhost:9117"})
	if err == nil {
		t.Error("expected error when api_key is missing")
	}
}

func TestDefaultIndexer(t *testing.T) {
	p := makePlugin(t, "http://localhost:9117", "key", nil)
	if len(p.indexers) != 1 || p.indexers[0] != "all" {
		t.Errorf("default indexer: got %v, want [all]", p.indexers)
	}
}

func TestPhase(t *testing.T) {
	p := makePlugin(t, "http://localhost:9117", "key", nil)
	if p.Phase() != plugin.PhaseSearch {
		t.Errorf("phase: got %q, want %q", p.Phase(), plugin.PhaseSearch)
	}
}

// --- search tests ---

func TestSearchSendsCorrectQueryParams(t *testing.T) {
	var gotQuery, gotAPIKey, gotCat string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotAPIKey = r.URL.Query().Get("apikey")
		gotCat = r.URL.Query().Get("cat")
		fmt.Fprint(w, torznabResponse(nil)) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "mykey", map[string]any{
		"categories": []any{"5000", "5030"},
	})
	_, err := p.Search(context.Background(), tc(), "Breaking Bad")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "Breaking Bad" {
		t.Errorf("q: got %q", gotQuery)
	}
	if gotAPIKey != "mykey" {
		t.Errorf("apikey: got %q", gotAPIKey)
	}
	if gotCat != "5000,5030" {
		t.Errorf("cat: got %q", gotCat)
	}
}

func TestSearchParsesEntries(t *testing.T) {
	items := []torznabItem{
		{
			Title: "Breaking.Bad.S01E01.720p.HDTV",
			Enclosure: struct{ URL string `xml:"url,attr"` }{
				URL: "http://tracker.example.com/1.torrent",
			},
			Size: 1_500_000_000,
			Attrs: []torznabAttr{
				{Name: "seeders", Value: "42"},
				{Name: "leechers", Value: "3"},
				{Name: "infohash", Value: "AABBCC"},
				{Name: "category", Value: "5030"},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, torznabResponse(items)) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), "Breaking Bad")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Title != "Breaking.Bad.S01E01.720p.HDTV" {
		t.Errorf("title: got %q", e.Title)
	}
	if e.URL != "http://tracker.example.com/1.torrent" {
		t.Errorf("url: got %q", e.URL)
	}
	if v := e.GetInt("torrent_seeders"); v != 42 {
		t.Errorf("torrent_seeders: got %d, want 42", v)
	}
	if v := e.GetInt("torrent_leechers"); v != 3 {
		t.Errorf("torrent_leechers: got %d, want 3", v)
	}
	if v := e.GetString("torrent_info_hash"); v != "aabbcc" {
		t.Errorf("torrent_info_hash: got %q, want aabbcc", v)
	}
	if v := e.GetInt("torrent_size"); v != 1_500_000_000 {
		t.Errorf("torrent_size: got %d", v)
	}
	if v := e.GetString("jackett_category"); v != "5030" {
		t.Errorf("jackett_category: got %q", v)
	}
	if v := e.GetString("jackett_indexer"); v != "all" {
		t.Errorf("jackett_indexer: got %q", v)
	}
}

func TestSearchDeduplicatesAcrossIndexers(t *testing.T) {
	// Both indexers return the same URL — should appear only once.
	body := torznabResponse([]torznabItem{
		{Title: "Show.S01E01", Link: "http://example.com/1"},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", map[string]any{
		"indexers": []any{"idx1", "idx2"},
	})
	entries, err := p.Search(context.Background(), tc(), "Show")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries after dedup, want 1", len(entries))
	}
}

func TestSearchSkipsFailingIndexer(t *testing.T) {
	// First indexer returns an error, second returns a result.
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, torznabResponse([]torznabItem{ //nolint:errcheck
			{Title: "Show.S01E01", Link: "http://example.com/1"},
		}))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", map[string]any{
		"indexers": []any{"bad", "good"},
	})
	entries, err := p.Search(context.Background(), tc(), "Show")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
}

func TestSearchEmptyFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, torznabResponse(nil)) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), "anything")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestCategoriesJoined(t *testing.T) {
	p := makePlugin(t, "http://localhost", "key", map[string]any{
		"categories": []any{"2000", "2010"},
	})
	if p.categories != "2000,2010" {
		t.Errorf("categories: got %q, want %q", p.categories, "2000,2010")
	}
}

func TestNoCategoriesOmitsParam(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.RawQuery
		fmt.Fprint(w, torznabResponse(nil)) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	_, _ = p.Search(context.Background(), tc(), "test")
	if strings.Contains(gotRaw, "cat=") {
		t.Errorf("cat param should be absent when no categories configured; got %q", gotRaw)
	}
}
