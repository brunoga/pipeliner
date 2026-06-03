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
	"sync"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

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

// permissiveCaps is a Torznab caps response that advertises every typed
// search mode with every typed param supported. Test handlers that don't
// care about caps return this so the plugin proceeds with whatever t=
// the test's query implies.
const permissiveCaps = `<?xml version="1.0"?>
<caps>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep,imdbid,tvdbid,tmdbid,year"/>
    <movie-search available="yes" supportedParams="q,imdbid,tmdbid,year"/>
  </searching>
</caps>`

// servesCaps wraps a search-result handler so it also responds to t=caps
// requests with permissiveCaps. Keeps existing tests working unchanged
// without each having to know about the caps probe the plugin now does
// before its first search per indexer.
func servesCaps(searchHandler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, permissiveCaps)
			return
		}
		searchHandler(w, r)
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

func TestName(t *testing.T) {
	p := makePlugin(t, "http://localhost:9117", "key", nil)
	if p.Name() != "jackett" {
		t.Errorf("Name: got %q, want jackett", p.Name())
	}
}

// --- search tests ---

func TestSearchSendsCorrectQueryParams(t *testing.T) {
	var gotQuery, gotAPIKey, gotCat string
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotAPIKey = r.URL.Query().Get("apikey")
		gotCat = r.URL.Query().Get("cat")
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "mykey", map[string]any{
		"categories": []any{"5000", "5030"},
	})
	_, err := p.Search(context.Background(), tc(), entry.New("Breaking Bad", ""))
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

func TestSearchSetsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	_, _ = p.Search(context.Background(), tc(), entry.New("test", ""))
	if gotUA != "pipeliner/1.0" {
		t.Errorf("User-Agent: got %q, want pipeliner/1.0", gotUA)
	}
}

func TestSearchParsesEntries(t *testing.T) {
	items := []torznabItem{
		{
			Title:     "Breaking.Bad.S01E01.720p.HDTV",
			Enclosure: struct{ URL string `xml:"url,attr"` }{URL: "http://tracker.example.com/1.torrent"},
			Size:      1_500_000_000,
			Attrs: []torznabAttr{
				{Name: "seeders", Value: "42"},
				{Name: "leechers", Value: "3"},
				{Name: "infohash", Value: "AABBCC"},
				{Name: "category", Value: "5030"},
			},
		},
	}
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, string(buildXML(items)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), entry.New("Breaking Bad", ""))
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
	if v := e.GetString(entry.FieldTitle); v != "Breaking.Bad.S01E01.720p.HDTV" {
		t.Errorf("FieldTitle: got %q", v)
	}
	if e.URL != "http://tracker.example.com/1.torrent" {
		t.Errorf("url: got %q", e.URL)
	}
	if v := e.GetInt(entry.FieldTorrentSeeds); v != 42 {
		t.Errorf("seeds: got %d, want 42", v)
	}
	if v := e.GetInt(entry.FieldTorrentLeechers); v != 3 {
		t.Errorf("leechers: got %d, want 3", v)
	}
	if v := e.GetString(entry.FieldTorrentInfoHash); v != "aabbcc" {
		t.Errorf("info_hash: got %q, want aabbcc", v)
	}
	if v := e.GetInt(entry.FieldTorrentFileSize); v != 1_500_000_000 {
		t.Errorf("file_size: got %d", v)
	}
	if v := e.GetString("jackett_category"); v != "5030" {
		t.Errorf("jackett_category: got %q", v)
	}
	if v := e.GetString(entry.FieldSource); v != "jackett:all" {
		t.Errorf("source: got %q, want \"jackett:all\"", v)
	}
}

func TestSearchQueriesAllIndexers(t *testing.T) {
	var mu sync.Mutex
	var gotPaths []string
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPaths = append(gotPaths, r.URL.Path)
		mu.Unlock()
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", map[string]any{
		"indexers": []any{"idx1", "idx2", "idx3"},
	})
	_, err := p.Search(context.Background(), tc(), entry.New("Show", ""))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(gotPaths) != 3 {
		t.Fatalf("expected 3 requests, got %d: %v", len(gotPaths), gotPaths)
	}
	for _, want := range []string{"idx1", "idx2", "idx3"} {
		found := false
		for _, path := range gotPaths {
			if strings.Contains(path, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no request found for indexer %q in paths: %v", want, gotPaths)
		}
	}
}

func TestSearchFailureReturnsNoEntriesNoError(t *testing.T) {
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), entry.New("Show", ""))
	if err != nil {
		t.Errorf("Search should not return error on indexer failure, got: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries on failure, got %d", len(entries))
	}
}

func TestSearchEmptyFeed(t *testing.T) {
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), entry.New("anything", ""))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestLimitSentAsQueryParam(t *testing.T) {
	var gotLimit string
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", map[string]any{"limit": int64(50)})
	_, err := p.Search(context.Background(), tc(), entry.New("test", ""))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotLimit != "50" {
		t.Errorf("limit: got %q, want \"50\"", gotLimit)
	}
}

func TestNoLimitOmitsParam(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.RawQuery
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	_, _ = p.Search(context.Background(), tc(), entry.New("test", ""))
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
	if p.client.Timeout != 2*time.Minute {
		t.Errorf("timeout: got %v, want 2m", p.client.Timeout)
	}
}

func TestTimeoutDefault(t *testing.T) {
	p := makePlugin(t, "http://localhost", "key", nil)
	if p.client.Timeout != 60*time.Second {
		t.Errorf("default timeout: got %v, want 60s", p.client.Timeout)
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

func TestCategoriesAsIntegers(t *testing.T) {
	p := makePlugin(t, "http://localhost", "key", map[string]any{
		"categories": []any{int64(2000), float64(5000)},
	})
	if p.categories != "2000,5000" {
		t.Errorf("categories: got %q, want \"2000,5000\"", p.categories)
	}
}

func TestNoCategoriesOmitsParam(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.RawQuery
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	_, _ = p.Search(context.Background(), tc(), entry.New("test", ""))
	if strings.Contains(gotRaw, "cat=") {
		t.Errorf("cat param should be absent when no categories configured; got %q", gotRaw)
	}
}

func TestSearchMagnetURLUsedWhenMagneturlAttrPresent(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd"
	items := []torznabItem{{
		Title:     "My.Show.S01E01.720p",
		Enclosure: struct{ URL string `xml:"url,attr"` }{URL: "https://jackett.host/dl/idx/?key=abc"},
		Attrs:     []torznabAttr{{Name: "magneturl", Value: magnet}},
	}}
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, string(buildXML(items)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), entry.New("My Show", ""))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.URL != magnet {
		t.Errorf("URL: got %q, want magnet URI", e.URL)
	}
	if v := e.GetString(entry.FieldTorrentLinkType); v != "magnet" {
		t.Errorf("torrent_link_type: got %q, want magnet", v)
	}
}

func TestSearchTorrentLinkTypeSetForNonMagnet(t *testing.T) {
	items := []torznabItem{{
		Title:     "My.Show.S01E01.720p",
		Enclosure: struct{ URL string `xml:"url,attr"` }{URL: "https://jackett.host/dl/idx/?key=abc"},
	}}
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, string(buildXML(items)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), entry.New("My Show", ""))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if v := entries[0].GetString(entry.FieldTorrentLinkType); v != "torrent" {
		t.Errorf("torrent_link_type: got %q, want torrent", v)
	}
}

// --- Generate (source mode) tests ---

func TestGenerateUsesEmptyQueryByDefault(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	if _, err := p.Generate(context.Background(), tc()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("default query should be empty, got %q", gotQuery)
	}
}

func TestGenerateUsesConfiguredQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, string(buildXML(nil)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", map[string]any{"query": "breaking bad"})
	if _, err := p.Generate(context.Background(), tc()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotQuery != "breaking bad" {
		t.Errorf("query: got %q, want %q", gotQuery, "breaking bad")
	}
}

func TestGenerateReturnsEntries(t *testing.T) {
	items := []torznabItem{
		{Title: "Breaking.Bad.S01E01.720p", Link: "http://example.com/1.torrent"},
		{Title: "The.Wire.S01E01.720p", Link: "http://example.com/2.torrent"},
	}
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, string(buildXML(items)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Generate(context.Background(), tc())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
}

// --- retry tests ---

func TestSearchRetriesOn5xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, string(buildXML([]torznabItem{
			{Title: "Show.S01E01", Link: "http://example.com/1.torrent"},
		})))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), entry.New("show", ""))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after retry, got %d", len(entries))
	}
}

func TestSearchNoRetryOn4xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	p.Search(context.Background(), tc(), entry.New("show", ""))
	if attempts != 1 {
		t.Errorf("expected 1 attempt for 4xx, got %d", attempts)
	}
}

func TestSearchNoRetryOnAPIError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		fmt.Fprint(w, `<?xml version="1.0"?><error code="100" description="Incorrect user credentials"/>`)
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	// Torznab API errors are permanent — Search logs them and returns empty.
	p.Search(context.Background(), tc(), entry.New("show", ""))
	if attempts != 1 {
		t.Errorf("expected 1 attempt for API error, got %d", attempts)
	}
}

// --- info-hash deduplication tests ---

func TestSearchDeduplicatesByInfoHash(t *testing.T) {
	// Two indexers return the same torrent (same hash) via different URLs.
	hash := "aabbccddeeff00112233445566778899aabbccdd"
	itemA := torznabItem{
		Title: "Show.S01E01",
		Link:  "http://indexer-a.example.com/1.torrent",
		Attrs: []torznabAttr{{Name: "infohash", Value: hash}, {Name: "seeders", Value: "10"}},
	}
	itemB := torznabItem{
		Title: "Show.S01E01",
		Link:  "http://indexer-b.example.com/2.torrent",
		Attrs: []torznabAttr{{Name: "infohash", Value: hash}, {Name: "seeders", Value: "5"}},
	}

	// Serve itemA from indexer-a and itemB from indexer-b.
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "indexer-a") {
			fmt.Fprint(w, string(buildXML([]torznabItem{itemA})))
		} else {
			fmt.Fprint(w, string(buildXML([]torznabItem{itemB})))
		}
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", map[string]any{
		"indexers": []any{"indexer-a", "indexer-b"},
	})
	entries, err := p.Search(context.Background(), tc(), entry.New("show", ""))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated entry, got %d", len(entries))
	}
	// The entry with more seeds (itemA, 10 seeds) should be kept.
	if got := entries[0].GetInt(entry.FieldTorrentSeeds); got != 10 {
		t.Errorf("expected higher-seed entry to be kept: seeds=%d", got)
	}
}

func TestSearchKeepsBothWhenHashesDiffer(t *testing.T) {
	items := []torznabItem{
		{Title: "Show.S01E01", Link: "http://example.com/1.torrent",
			Attrs: []torznabAttr{{Name: "infohash", Value: "aaaa"}}},
		{Title: "Show.S01E02", Link: "http://example.com/2.torrent",
			Attrs: []torznabAttr{{Name: "infohash", Value: "bbbb"}}},
	}
	srv := httptest.NewServer(servesCaps(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, string(buildXML(items)))
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	entries, err := p.Search(context.Background(), tc(), entry.New("show", ""))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries with distinct hashes, got %d", len(entries))
	}
}
