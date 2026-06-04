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
		// Search page with no hoverlink results.
		w.Write([]byte("<html><body>no results</body></html>"))
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

func TestProcess_SkipsRejectedAndFailedEntries(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	pp := newProcessor(t, srv.URL)
	rejected := entry.New("Avatar", "")
	rejected.Reject("test")
	failed := entry.New("Inception", "")
	failed.Fail("test")

	_, err := pp.Process(context.Background(), taskCtx(), []*entry.Entry{rejected, failed})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if hits != 0 {
		t.Errorf("HTTP hits: got %d, want 0 (skipped entries)", hits)
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

func containsString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
