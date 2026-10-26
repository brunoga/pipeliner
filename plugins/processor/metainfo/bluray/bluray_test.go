package bluray

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	ibluray "github.com/brunoga/pipeliner/internal/bluray"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func tempStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	db, err := store.OpenSQLite(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func taskCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

func newProcessor(t *testing.T, srvURL string) *processorPlugin {
	t.Helper()
	p, err := newPlugin(map[string]any{}, tempStore(t))
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	pp := p.(*processorPlugin)
	pp.client = ibluray.New(
		ibluray.WithBaseURL(srvURL),
		ibluray.WithRequestInterval(0),
		ibluray.WithCountry("us"),
	)
	return pp
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", "bluray", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func TestProcess_ByExistingID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/movies/Avatar-3D-Blu-ray/26954/" {
			w.Write(readFixture(t, "release_detail_avatar3d.html"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	pp := newProcessor(t, srv.URL)
	e := entry.New("Avatar (2009)", "https://example.com/avatar")
	e.Set(entry.FieldBlurayID, "26954")
	// Pre-seed siblings so the 3D-release boolean lights up AND so resolve can
	// look up the canonical slug for the fast-path detail fetch.
	pp.indexCache.Set(indexKey("Avatar"), []ibluray.IndexEntry{
		{ID: "7847", Slug: "Avatar-Blu-ray", Format: ibluray.FormatBD, Year: 2009, Title: "Avatar"},
		{ID: "26954", Slug: "Avatar-3D-Blu-ray", Format: ibluray.FormatBD3D, Year: 2009, Title: "Avatar 3D"},
	})

	out, err := pp.Process(context.Background(), taskCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Process: got %d entries, want 1", len(out))
	}
	if v, _ := e.Fields[entry.FieldBlurayCodec].(string); v != "MPEG-4 MVC" {
		t.Errorf("codec: got %q, want MPEG-4 MVC", v)
	}
	if v, _ := e.Fields[entry.FieldBlurayIs3DEdition].(bool); !v {
		t.Errorf("bluray_is_3d_edition: got false, want true")
	}
	if v, _ := e.Fields[entry.FieldBluray3DRelease].(bool); !v {
		t.Errorf("bluray_3d_release: got false, want true")
	}
	if v, _ := e.Fields[entry.FieldBlurayReleaseDate].(string); v != "2012-10-16" {
		t.Errorf("release_date: got %q, want 2012-10-16", v)
	}
	if v, _ := e.Fields[entry.FieldEnriched].(bool); !v {
		t.Errorf("enriched: got false, want true")
	}
}

func TestProcess_BySearchFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/":
			w.Write(readFixture(t, "search_avatar.html"))
		case "/movies/Avatar-Blu-ray/7847/":
			w.Write(readFixture(t, "release_detail_avatar3d.html")) // any detail page
		case "/movies/Avatar-3D-Blu-ray/26954/":
			w.Write(readFixture(t, "release_detail_avatar3d.html"))
		default:
			// Other Avatar detail URLs the search returns. For each, serve the
			// 3D detail page so the test only asserts on the fields we control.
			if r.URL.Path != "" && len(r.URL.Path) > len("/movies/") {
				w.Write(readFixture(t, "release_detail_avatar3d.html"))
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pp := newProcessor(t, srv.URL)
	e := entry.New("Avatar (2009)", "https://example.com/avatar")
	e.Set(entry.FieldVideoYear, int(2009))

	_, err := pp.Process(context.Background(), taskCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if v, _ := e.Fields[entry.FieldBlurayID].(string); v == "" {
		t.Error("bluray_id not set after search fallback")
	}
	// 3D detection should fire because the search results include a -3D-Blu-ray entry.
	if v, _ := e.Fields[entry.FieldBluray3DRelease].(bool); !v {
		t.Error("bluray_3d_release: got false, want true (Avatar has BD3D release in catalog)")
	}
	// Search results were cached.
	if hits, ok := pp.indexCache.Get(indexKey("Avatar")); !ok || len(hits) == 0 {
		t.Error("indexCache empty after search fallback")
	}
}

func TestProcess_RespectsNegativeCache(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	pp := newProcessor(t, srv.URL)
	pp.negCache.Set(indexKey("ObscureMovie"), time.Now())

	e := entry.New("ObscureMovie", "https://example.com/x")
	_, err := pp.Process(context.Background(), taskCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if hits != 0 {
		t.Errorf("HTTP hits: got %d, want 0 (negative cache should have short-circuited)", hits)
	}
	if _, ok := e.Fields[entry.FieldBlurayID]; ok {
		t.Error("bluray_id set on negative-cache hit; entry should be untouched")
	}
}

func TestProcess_SearchMissCachesNegative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A real /search/ response with the canonical title marker but no
		// hoverlink results — the legitimate "no results found" case.
		w.Write([]byte(`<html><head><title>Blu-ray.com - Search</title></head>` +
			`<body>No results.</body></html>`))
	}))
	defer srv.Close()

	pp := newProcessor(t, srv.URL)
	e := entry.New("NotReal", "https://example.com/x")
	_, err := pp.Process(context.Background(), taskCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if _, ok := pp.negCache.Get(indexKey("NotReal")); !ok {
		t.Error("negCache not populated after empty search")
	}
}

// TestProcess_NonSearchPageDoesNotCacheNegative confirms that a 200 response with
// a body that does not look like a search results page (e.g. an unexpected error
// page or a markup change we have not adapted to) is treated as a search failure —
// surfaced as a Warn log and NOT written to the negative cache.
func TestProcess_NonSearchPageDoesNotCacheNegative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HTTP 200 with body that lacks the "<title>Blu-ray.com - Search"
		// marker — simulates an unexpected page.
		w.Write([]byte(`<html><head><title>Just a moment...</title></head>` +
			`<body>Checking your browser</body></html>`))
	}))
	defer srv.Close()

	pp := newProcessor(t, srv.URL)
	e := entry.New("Avatar (2009)", "https://example.com/x")
	if _, err := pp.Process(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if _, ok := pp.negCache.Get(indexKey("Avatar")); ok {
		t.Error("negCache populated despite non-search response; should NOT cache negative")
	}
	if _, ok := e.Fields[entry.FieldBlurayID]; ok {
		t.Error("bluray_id set despite non-search response; entry should be untouched")
	}
}

func TestPickBest_PrefersBDOverBD3D(t *testing.T) {
	hits := []ibluray.IndexEntry{
		{ID: "26954", Format: ibluray.FormatBD3D, Year: 2009},
		{ID: "7847", Format: ibluray.FormatBD, Year: 2009},
		{ID: "349437", Format: ibluray.FormatUHD, Year: 2009},
	}
	best := pickBest(hits, 2009)
	if best.ID != "7847" {
		t.Errorf("pickBest preferred %q (%s), want 7847 (BD)", best.ID, best.Format)
	}
}

func TestPickBest_YearFilter(t *testing.T) {
	hits := []ibluray.IndexEntry{
		{ID: "100", Format: ibluray.FormatBD, Year: 1999},
		{ID: "200", Format: ibluray.FormatBD3D, Year: 2009},
	}
	best := pickBest(hits, 2009)
	if best.ID != "200" {
		t.Errorf("pickBest ignored year hint; got %q, want 200", best.ID)
	}
}

func TestSearchTitle_Preference(t *testing.T) {
	e := entry.New("Avatar.2009.1080p.BluRay.x264", "")
	e.Set(entry.FieldMovieTitle, "Avatar")
	if got := searchTitle(e); got != "Avatar" {
		t.Errorf("searchTitle preferred movie_title? got %q, want Avatar", got)
	}

	e2 := entry.New("Avatar.2009.1080p.BluRay.x264", "")
	if got := searchTitle(e2); got != "Avatar" {
		t.Errorf("searchTitle fell back to movies.Parse? got %q, want Avatar", got)
	}
}

func TestValidate_RejectsUnknownKeys(t *testing.T) {
	errs := validate(map[string]any{"bogus": "x"})
	var found bool
	for _, e := range errs {
		if e != nil && containsString(e.Error(), "bogus") {
			found = true
		}
	}
	if !found {
		t.Errorf("validate: expected unknown-key error, got %v", errs)
	}
}

func TestProcess_ExtendedFieldsAndCheckMovieHas3DFallback(t *testing.T) {
	// Simulate a 2026 movie where quicksearch (/search/?section=bluraymovies) returns
	// empty and the embedded 3D catalog does not yet have it, so metainfo_bluray falls
	// back to CheckMovieHas3D (theatrical search -> movie detail -> /products/menu_ajax.php)
	// and then fetches the release detail.
	theatricalHTML := `<!DOCTYPE html><html><head><title>Blu-ray.com - Search</title></head><body>
<h4>1 match for "Custom3DMovie"</h4>
<div class="figure">
	<a class="alphaborder hoverlink" data-globalproductid="128351" href="https://www.blu-ray.com/Custom3DMovie/128351/" alt="Custom3DMovie (2026)"></a>
	<div class="figurecaptiontop"><span class="fa fa-star"> 8.1</span></div>
	<div class="figurecaptionbottom"><div>Custom3DMovie</div><div>2026</div></div>
</div>
</body></html>`

	movieHTML := `<!DOCTYPE html><html><body>
<div class="blackbox"><h1 class="eurostile">Custom3DMovie</h1><font class="oswald">2026</font></div>
<div id="content_overview">
	<table>
		<tr><td class="specmenu">Technical details</td><td class="specitem">2.39:1<br><span>Native 3D, 110 mins</span><br></td></tr>
	</table>
</div>
</body></html>`

	releasesAllHTML := `<table><tr><td>
<h3>3D Blu-ray Editions (1)</h3><br>
<img src="https://images.static-bluray.com/flags/DE.png" title="Germany">
<a href="https://www.blu-ray.com/movies/Custom3DMovie-3D-Blu-ray/999888/" title="Custom3DMovie 3D">Custom3DMovie 3D</a>
<small style="color: #999999">Turbine Medien</small><br>
</td></tr></table>`

	releaseDetailHTML := `<!DOCTYPE html><html><body>
<table><tr><td width="518">
<a class="black noline" data-globalparentid="128351" href="https://www.blu-ray.com/Custom3DMovie/128351/"><h1>Custom3DMovie 3D Blu-ray</h1></a><img src="https://images.static-bluray.com/flags/DE.png" title="Germany">
</td></tr></table>
<span class="subheadingtitle">Blu-ray 3D + Blu-ray</span>
<span class="subheading grey">Turbine Medien | 2026 | 110 min | Rated PG-13 | Jan 15, 2026</span>
<img class="coverfront" id="frontimage_overlay" src="https://images.static-bluray.com/movies/covers/999888_medium.jpg">
<table><tr>
<td width="228px">
	<span class="subheading">Video</span><br>
	Codec: MPEG-4 MVC (28.00 Mbps)<br>
	Resolution: 1080p<br>
	Aspect ratio: 2.39:1<br>
	<span class="subheading">Audio</span><br>
	<div id="longaudio">English: Dolby Atmos<br>German: DTS-HD Master Audio 5.1<br>(less)</div>
	<span class="subheading">Subtitles</span><br>
	<div id="longsubs">English, German (less)</div>
	<span class="subheading">Discs</span><br>
	Blu-ray 3D<br>Single disc (1 BD-50)<br>
	<span class="subheading">Playback</span><br>
	2K Blu-ray: Region free<br>
</td>
<td width="266px">
	<div id="ratingscore_128351_0">8.1</div>
	<div id="review_rating"><table><tr><td>Overall</td><td></td><td><b>4.5</b></td></tr></table></div>
	<div id="user_rating"><table><tr><td>Overall</td><td></td><td><b>4.3</b></td></tr></table></div>
</td>
</tr></table>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/":
			if r.URL.Query().Get("section") == "theatrical" {
				w.Write([]byte(theatricalHTML))
				return
			}
			w.Write([]byte(`<html><head><title>Blu-ray.com - Search</title></head><body>No results.</body></html>`))
		case "/Movie/128351/", "/Custom3DMovie/128351/":
			w.Write([]byte(movieHTML))
		case "/products/menu_ajax.php":
			w.Write([]byte(releasesAllHTML))
		case "/movies/Custom3DMovie-3D-Blu-ray/999888/":
			w.Write([]byte(releaseDetailHTML))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := newPlugin(map[string]any{"country": "all"}, tempStore(t))
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	pp := p.(*processorPlugin)
	pp.client = ibluray.New(
		ibluray.WithBaseURL(srv.URL),
		ibluray.WithRequestInterval(0),
		ibluray.WithCountry("all"),
	)

	e := entry.New("Custom3DMovie (2026)", "https://example.com/custom3d")
	e.Set(entry.FieldVideoYear, 2026)

	_, err = pp.Process(context.Background(), taskCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if got := e.GetString(entry.FieldBlurayID); got != "999888" {
		t.Errorf("bluray_id: got %q, want 999888", got)
	}
	if got := e.GetString(entry.FieldBlurayParentID); got != "128351" {
		t.Errorf("bluray_parent_id: got %q, want 128351", got)
	}
	if got, _ := e.Fields[entry.FieldBlurayNative3D].(bool); !got {
		t.Errorf("bluray_native_3d: got false, want true")
	}
	if got, _ := e.Fields[entry.FieldBluray3DRelease].(bool); !got {
		t.Errorf("bluray_3d_release: got false, want true")
	}
	if got, _ := e.Fields[entry.FieldBlurayAudio].([]string); len(got) != 2 {
		t.Errorf("bluray_audio: got %v, want 2 tracks", got)
	}
	if got, _ := e.Fields[entry.FieldBluraySubtitles].([]string); len(got) != 2 {
		t.Errorf("bluray_subtitles: got %v, want 2 languages", got)
	}
	if got := e.GetString(entry.FieldBlurayPlayback); got != "2K Blu-ray: Region free" {
		t.Errorf("bluray_playback: got %q, want '2K Blu-ray: Region free'", got)
	}
	if got, _ := e.Fields[entry.FieldBlurayRating].(float64); got != 8.1 {
		t.Errorf("bluray_rating: got %v, want 8.1", got)
	}
	if got, _ := e.Fields[entry.FieldBlurayReviewRating].(float64); got != 4.5 {
		t.Errorf("bluray_review_rating: got %v, want 4.5", got)
	}
	if got, _ := e.Fields[entry.FieldBlurayUserRating].(float64); got != 4.3 {
		t.Errorf("bluray_user_rating: got %v, want 4.3", got)
	}
}

func TestProcess_Embedded3DRescueWhenCountrySearchEmpty(t *testing.T) {
	// Even if country-scoped /search/ returns 0 hits and detail fetch 404s,
	// a title present in the embedded worldwide 3D catalog (e.g. Dredd 2012)
	// must be rescued from the embedded catalog and NOT negative-cached.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/" {
			w.Write([]byte(`<html><head><title>Blu-ray.com - Search</title></head><body>No results.</body></html>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	pp := newProcessor(t, srv.URL)
	e := entry.New("Dredd (2012)", "https://example.com/dredd")
	e.Set(entry.FieldVideoYear, 2012)

	_, err := pp.Process(context.Background(), taskCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got, _ := e.Fields[entry.FieldBluray3DRelease].(bool); !got {
		t.Error("bluray_3d_release: got false, want true (rescued from embedded 3D catalog)")
	}
	if _, neg := pp.negCache.Get(indexKey("Dredd")); neg {
		t.Error("negCache populated for Dredd despite embedded 3D catalog match")
	}
}

func containsString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
