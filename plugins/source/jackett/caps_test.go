package jackett

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

func TestParseCaps(t *testing.T) {
	data := []byte(`<?xml version="1.0"?>
<caps>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep,imdbid,tvdbid"/>
    <movie-search available="no"/>
  </searching>
</caps>`)
	c, err := parseCaps(data)
	if err != nil {
		t.Fatalf("parseCaps: %v", err)
	}
	if !c.search.available {
		t.Error("search should be available")
	}
	if !c.tvSearch.available {
		t.Error("tv-search should be available")
	}
	if c.movieSearch.available {
		t.Error("movie-search should be unavailable")
	}
	if !c.tvSearch.supports("season") || !c.tvSearch.supports("imdbid") {
		t.Error("tv-search supportedParams not parsed")
	}
	if c.tvSearch.supports("year") {
		t.Error("year is not in tv-search supportedParams")
	}
	// movie-search has no supportedParams → params map is nil → supports()
	// returns true (conservative default for "unknown").
	if !c.movieSearch.supports("year") {
		t.Error("modeCaps with nil params should default supports() to true")
	}
}

func TestParseCapsRejectsNonCapsXML(t *testing.T) {
	if _, err := parseCaps(buildXML(nil)); err == nil {
		t.Error("expected parseCaps to reject an <rss> document")
	}
}

// TestSearchUsesCapsToPickMode confirms that when caps advertise that
// movie-search is unavailable, the plugin sends a generic t=search query
// instead of t=movie — which is what fixed the 3dtorrents emergency.
func TestSearchUsesCapsToPickMode(t *testing.T) {
	const capsNoMovie = `<?xml version="1.0"?>
<caps>
  <searching>
    <search available="yes" supportedParams="q"/>
    <movie-search available="no"/>
    <tv-search available="no"/>
  </searching>
</caps>`

	var mu sync.Mutex
	var searchT, searchYear string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, capsNoMovie) //nolint:errcheck
			return
		}
		mu.Lock()
		searchT = r.URL.Query().Get("t")
		searchYear = r.URL.Query().Get("year")
		mu.Unlock()
		fmt.Fprint(w, string(buildXML(nil))) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	e := entry.New("Inception", "")
	e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
	e.Set(entry.FieldVideoYear, 2010)
	if _, err := p.Search(context.Background(), tc(), e); err != nil {
		t.Fatalf("Search: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if searchT != "search" {
		t.Errorf("t: got %q, want \"search\" (caps say movie-search unavailable)", searchT)
	}
	if searchYear != "" {
		t.Errorf("year should be omitted in generic search, got %q", searchYear)
	}
}

// TestSearchFallsBackOnTorznabError201 covers the safety net: an indexer
// whose caps lie (or whose caps fetch is bypassed) and which returns
// torznab error 201 for a typed query should be retried with t=search.
func TestSearchFallsBackOnTorznabError201(t *testing.T) {
	const errResp = `<?xml version="1.0"?><error code="201" description="does not support the requested query"/>`

	var (
		mu        sync.Mutex
		movieHits int
		searchHit bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			// Caps say everything works — to force the 201 path.
			fmt.Fprint(w, permissiveCaps) //nolint:errcheck
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Query().Get("t") {
		case "movie":
			movieHits++
			fmt.Fprint(w, errResp) //nolint:errcheck
		case "search":
			searchHit = true
			fmt.Fprint(w, string(buildXML([]torznabItem{
				{Title: "Inception", Link: "http://example.com/1.torrent"},
			}))) //nolint:errcheck
		default:
			t.Errorf("unexpected t=%q", r.URL.Query().Get("t"))
		}
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	e := entry.New("Inception", "")
	e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
	entries, err := p.Search(context.Background(), tc(), e)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if movieHits != 1 {
		t.Errorf("t=movie hits: got %d, want 1 (no retry on permanent 201)", movieHits)
	}
	if !searchHit {
		t.Error("expected fallback t=search request after 201")
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry from fallback path, got %d", len(entries))
	}
}

// TestSearchDoesNotFallBackWhenTSearchAlsoFails ensures we don't loop —
// a 201 on t=search (which would be malformed indexer behaviour) is
// surfaced rather than retried again.
func TestSearchDoesNotFallBackOnSearchAlreadyGeneric(t *testing.T) {
	const errResp = `<?xml version="1.0"?><error code="201" description="bad"/>`

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			fmt.Fprint(w, permissiveCaps) //nolint:errcheck
			return
		}
		hits.Add(1)
		fmt.Fprint(w, errResp) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	// No media_type → t=search from the start.
	_, _ = p.Search(context.Background(), tc(), entry.New("anything", ""))
	if got := hits.Load(); got != 1 {
		t.Errorf("expected 1 search hit (no fallback loop), got %d", got)
	}
}

// TestSearchCapsCached confirms caps are fetched once per indexer and
// reused across Search calls, so a long-running pipeline doesn't probe
// caps every interval.
func TestSearchCapsCached(t *testing.T) {
	var capsHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			capsHits.Add(1)
			fmt.Fprint(w, permissiveCaps) //nolint:errcheck
			return
		}
		fmt.Fprint(w, string(buildXML(nil))) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	for range 3 {
		if _, err := p.Search(context.Background(), tc(), entry.New("x", "")); err != nil {
			t.Fatalf("Search: %v", err)
		}
	}
	if got := capsHits.Load(); got != 1 {
		t.Errorf("caps hits: got %d, want 1 (should be cached)", got)
	}
}

// TestSearchCapsRetriedAfterFailure confirms caps fetch failures are NOT
// cached — the next Search call retries the caps probe rather than
// permanently treating the indexer as "no caps known".
func TestSearchCapsRetriedAfterFailure(t *testing.T) {
	var capsHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			capsHits.Add(1)
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, string(buildXML(nil))) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL, "key", nil)
	for range 2 {
		_, _ = p.Search(context.Background(), tc(), entry.New("x", ""))
	}
	if got := capsHits.Load(); got < 2 {
		t.Errorf("caps hits: got %d, want >=2 (failures must not be cached)", got)
	}
}

func TestModeCapsSupportsUnknownParamsAccepted(t *testing.T) {
	// No supportedParams attribute → everything is accepted (conservative).
	mc := modeCaps{available: true}
	if !mc.supports("anything") {
		t.Error("modeCaps with nil params map should accept any param")
	}
}

func TestModeCapsSupportsListedOnly(t *testing.T) {
	mc := modeCaps{available: true, params: map[string]bool{"q": true, "year": true}}
	if !mc.supports("year") {
		t.Error("year should be supported")
	}
	if mc.supports("imdbid") {
		t.Error("imdbid should not be supported")
	}
}

// TestParseCapsAvailableIsCaseInsensitive covers indexers that emit
// available="YES" / available="Yes" — toModeCaps uses EqualFold so any
// casing is accepted.
func TestParseCapsAvailableIsCaseInsensitive(t *testing.T) {
	data := []byte(`<?xml version="1.0"?>
<caps>
  <searching>
    <search available="YES"/>
    <tv-search available="Yes"/>
    <movie-search available="no"/>
  </searching>
</caps>`)
	c, err := parseCaps(data)
	if err != nil {
		t.Fatalf("parseCaps: %v", err)
	}
	if !c.search.available || !c.tvSearch.available {
		t.Error("YES / Yes should both be treated as available")
	}
	if c.movieSearch.available {
		t.Error(`"no" should not be treated as available`)
	}
}
