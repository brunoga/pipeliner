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
	p, err := newPlugin(cfg, nil)
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
	_, err := newPlugin(map[string]any{"api_key": "key"}, nil)
	if err == nil {
		t.Error("expected error when url is missing")
	}
}

func TestMissingAPIKey(t *testing.T) {
	_, err := newPlugin(map[string]any{"url": "http://localhost:9117"}, nil)
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
	if p.Phase() != plugin.PhaseFrom {
		t.Errorf("phase: got %q, want %q", p.Phase(), plugin.PhaseFrom)
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
	if v := e.GetInt("seeds"); v != 42 {
		t.Errorf("seeds: got %d, want 42", v)
	}
	if v := e.GetInt("leechers"); v != 3 {
		t.Errorf("leechers: got %d, want 3", v)
	}
	if v := e.GetString("info_hash"); v != "aabbcc" {
		t.Errorf("info_hash: got %q, want aabbcc", v)
	}
	if v := e.GetInt("file_size"); v != 1_500_000_000 {
		t.Errorf("file_size: got %d", v)
	}
	if v := e.GetString("jackett_category"); v != "5030" {
		t.Errorf("jackett_category: got %q", v)
	}
	if v := e.GetString("jackett_indexer"); v != "all" {
		t.Errorf("jackett_indexer: got %q", v)
	}
}

func TestSearchMultipleIndexersSentAsOneCall(t *testing.T) {
	// Multiple indexers should be joined and sent in a single API call.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, torznabResponse(nil)) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", map[string]any{
		"indexers": []any{"idx1", "idx2", "idx3"},
	})
	_, err := p.Search(context.Background(), tc(), "Show")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(gotPath, "idx1,idx2,idx3") {
		t.Errorf("expected comma-joined indexers in path, got %q", gotPath)
	}
}

func TestSearchFailureReturnsNoEntriesNoError(t *testing.T) {
	// A failed call logs a warning and returns (nil, nil) — no error propagated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), "Show")
	if err != nil {
		t.Errorf("Search should not return error on failure, got: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries on failure, got %d", len(entries))
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

func TestLimitSentAsQueryParam(t *testing.T) {
	var gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		fmt.Fprint(w, torznabResponse(nil)) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", map[string]any{"limit": int64(50)})
	_, err := p.Search(context.Background(), tc(), "test")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotLimit != "50" {
		t.Errorf("limit: got %q, want \"50\"", gotLimit)
	}
}

func TestNoLimitOmitsParam(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.RawQuery
		fmt.Fprint(w, torznabResponse(nil)) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil) // no limit configured
	_, _ = p.Search(context.Background(), tc(), "test")
	if strings.Contains(gotRaw, "limit=") {
		t.Errorf("limit param should be absent when not configured; got %q", gotRaw)
	}
}

func TestLimitDefaultIsZero(t *testing.T) {
	p := makePlugin(t, "http://localhost", "key", nil)
	if p.limit != 0 {
		t.Errorf("default limit: got %d, want 0", p.limit)
	}
}

func TestValidateLimitRejectsZero(t *testing.T) {
	errs := validate(map[string]any{"url": "http://localhost", "api_key": "key", "limit": int64(0)})
	if len(errs) == 0 {
		t.Error("expected error for limit=0")
	}
}

func TestValidateLimitRejectsNegative(t *testing.T) {
	errs := validate(map[string]any{"url": "http://localhost", "api_key": "key", "limit": int64(-5)})
	if len(errs) == 0 {
		t.Error("expected error for limit=-5")
	}
}

func TestValidateLimitAcceptsPositive(t *testing.T) {
	errs := validate(map[string]any{"url": "http://localhost", "api_key": "key", "limit": int64(100)})
	if len(errs) != 0 {
		t.Errorf("unexpected errors for valid limit: %v", errs)
	}
}

func TestValidateLimitAbsentIsOk(t *testing.T) {
	errs := validate(map[string]any{"url": "http://localhost", "api_key": "key"})
	if len(errs) != 0 {
		t.Errorf("unexpected errors when limit absent: %v", errs)
	}
}

func TestTimeoutConfigured(t *testing.T) {
	p := makePlugin(t, "http://localhost", "key", map[string]any{"timeout": "2m"})
	if p.timeout != 2*60*1000*1000*1000 {
		t.Errorf("timeout: got %v, want 2m", p.timeout)
	}
}

func TestTimeoutDefault(t *testing.T) {
	p := makePlugin(t, "http://localhost", "key", nil)
	if p.timeout != 60*1000*1000*1000 {
		t.Errorf("default timeout: got %v, want 60s", p.timeout)
	}
}

func TestValidateTimeoutRejectsInvalid(t *testing.T) {
	errs := validate(map[string]any{"url": "http://localhost", "api_key": "key", "timeout": "notaduration"})
	if len(errs) == 0 {
		t.Error("expected error for invalid timeout")
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

// --- jackett_input tests ---

func TestInputPluginPhaseAndName(t *testing.T) {
	cfg := map[string]any{"url": "http://localhost:9117", "api_key": "key"}
	p, err := newInputPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newInputPlugin: %v", err)
	}
	if p.Phase() != plugin.PhaseInput {
		t.Errorf("phase: got %q, want %q", p.Phase(), plugin.PhaseInput)
	}
	if p.Name() != "jackett_input" {
		t.Errorf("name: got %q, want jackett_input", p.Name())
	}
}

func TestInputPluginRunUsesEmptyQueryByDefault(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, torznabResponse(nil)) //nolint:errcheck
	}))
	defer srv.Close()

	p, err := newInputPlugin(map[string]any{"url": srv.URL, "api_key": "key"}, nil)
	if err != nil {
		t.Fatalf("newInputPlugin: %v", err)
	}
	if _, err := p.(*jackettInputPlugin).Run(context.Background(), tc()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("default query should be empty, got %q", gotQuery)
	}
}

func TestInputPluginRunUsesConfiguredQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, torznabResponse(nil)) //nolint:errcheck
	}))
	defer srv.Close()

	p, err := newInputPlugin(map[string]any{"url": srv.URL, "api_key": "key", "query": "breaking bad"}, nil)
	if err != nil {
		t.Fatalf("newInputPlugin: %v", err)
	}
	if _, err := p.(*jackettInputPlugin).Run(context.Background(), tc()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotQuery != "breaking bad" {
		t.Errorf("query: got %q, want %q", gotQuery, "breaking bad")
	}
}

func TestInputPluginRunReturnsEntries(t *testing.T) {
	items := []torznabItem{
		{Title: "Breaking.Bad.S01E01.720p", Link: "http://example.com/1.torrent"},
		{Title: "The.Wire.S01E01.720p", Link: "http://example.com/2.torrent"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, torznabResponse(items)) //nolint:errcheck
	}))
	defer srv.Close()

	p, err := newInputPlugin(map[string]any{"url": srv.URL, "api_key": "key"}, nil)
	if err != nil {
		t.Fatalf("newInputPlugin: %v", err)
	}
	entries, err := p.(*jackettInputPlugin).Run(context.Background(), tc())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
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
