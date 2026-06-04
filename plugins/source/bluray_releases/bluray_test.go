package bluray_releases

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/bluray"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func tempStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	db, err := store.OpenSQLite(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
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

func newSourceWithServer(t *testing.T, srvURL string, cfg map[string]any) *sourcePlugin {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	p, err := newPlugin(cfg, tempStore(t))
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	sp := p.(*sourcePlugin)
	sp.client = bluray.New(
		bluray.WithBaseURL(srvURL),
		bluray.WithRequestInterval(0),
		bluray.WithCountry("us"),
	)
	return sp
}

func TestWindows_MonthsBack(t *testing.T) {
	sp := &sourcePlugin{months: 3}
	got := sp.windows(time.Date(2025, 12, 15, 0, 0, 0, 0, time.UTC))
	want := []window{{2025, 10}, {2025, 11}, {2025, 12}}
	if len(got) != len(want) {
		t.Fatalf("windows: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("windows[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestWindows_YearBoundary(t *testing.T) {
	sp := &sourcePlugin{months: 3}
	got := sp.windows(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC))
	want := []window{{2025, 11}, {2025, 12}, {2026, 1}}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("windows[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestWindows_ExplicitFromTo(t *testing.T) {
	sp := &sourcePlugin{fromYear: 2025, fromMonth: 6, toYear: 2025, toMonth: 8}
	got := sp.windows(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	want := []window{{2025, 6}, {2025, 7}, {2025, 8}}
	if len(got) != len(want) {
		t.Fatalf("windows: got %v, want %v", got, want)
	}
}

func TestIndexKey_StripsFormatTokens(t *testing.T) {
	cases := []struct {
		title, key string
	}{
		{"Avatar", "avatar"},
		{"Avatar 3D", "avatar"},
		{"Avatar 4K", "avatar"},
		{"Avatar: The Way of Water 3D", "avatar: the way of water"},
	}
	for _, tc := range cases {
		if got := indexKey(tc.title); got != tc.key {
			t.Errorf("indexKey(%q) = %q, want %q", tc.title, got, tc.key)
		}
	}
}

func TestParseFormats_Default(t *testing.T) {
	f := parseFormats(nil)
	for _, want := range []bluray.Format{bluray.FormatBD, bluray.FormatUHD, bluray.FormatBD3D} {
		if !f[want] {
			t.Errorf("default formats missing %q", want)
		}
	}
}

func TestParseFormats_Aliases(t *testing.T) {
	f := parseFormats([]string{"3D", "4K"})
	if !f[bluray.FormatBD3D] || !f[bluray.FormatUHD] {
		t.Errorf("alias formats: got %v, want BD3D and UHD set", f)
	}
}

func TestGenerate_PopulatesIndexAndEmitsEntries(t *testing.T) {
	body, err := os.ReadFile("../../../internal/bluray/testdata/calendar_2025_12.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	sp := newSourceWithServer(t, srv.URL, map[string]any{
		"from_year": int64(2025), "from_month": int64(12),
		"to_year": int64(2025), "to_month": int64(12),
	})

	entries, err := sp.Generate(context.Background(), taskCtx())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) < 20 {
		t.Fatalf("entries: got %d, want >= 20", len(entries))
	}

	for _, e := range entries {
		if _, ok := e.Fields[entry.FieldBlurayID]; !ok {
			t.Errorf("entry %q missing bluray_id", e.Title)
		}
		if _, ok := e.Fields[entry.FieldBlurayFormat]; !ok {
			t.Errorf("entry %q missing bluray_format", e.Title)
		}
		if v, _ := e.Fields[entry.FieldMediaType].(string); v != entry.MediaTypeMovie {
			t.Errorf("entry %q media_type: got %q, want movie", e.Title, v)
		}
	}

	// Index should have been populated.
	var sampleKey string
	for _, e := range entries {
		sampleKey = indexKey(e.Title)
		if sampleKey != "" {
			break
		}
	}
	if hits, ok := sp.indexCache.Get(sampleKey); !ok || len(hits) == 0 {
		t.Errorf("indexCache empty for key %q after Generate", sampleKey)
	}
}

func TestGenerate_FormatFilter(t *testing.T) {
	body, err := os.ReadFile("../../../internal/bluray/testdata/calendar_2025_12.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	sp := newSourceWithServer(t, srv.URL, map[string]any{
		"from_year": int64(2025), "from_month": int64(12),
		"to_year": int64(2025), "to_month": int64(12),
		"formats": []any{"UHD"},
	})
	entries, err := sp.Generate(context.Background(), taskCtx())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("UHD filter: no entries, expected some")
	}
	for _, e := range entries {
		if got, _ := e.Fields[entry.FieldBlurayFormat].(string); got != string(bluray.FormatUHD) {
			t.Errorf("entry %q format: got %q, want UHD", e.Title, got)
		}
	}
}

func TestSearch_IndexHit(t *testing.T) {
	sp := newSourceWithServer(t, "http://invalid.invalid", nil)
	// Pre-populate the index with a fake Avatar entry.
	sp.indexCache.Set(indexKey("Avatar"), []bluray.IndexEntry{
		{ID: "26954", Slug: "Avatar-3D-Blu-ray", Title: "Avatar 3D", Format: bluray.FormatBD3D, Year: 2009},
		{ID: "7847", Slug: "Avatar-Blu-ray", Title: "Avatar", Format: bluray.FormatBD, Year: 2009},
	})

	qe := entry.New("Avatar", "")
	results, err := sp.Search(context.Background(), taskCtx(), qe)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Search: got %d results, want 2", len(results))
	}
	for _, r := range results {
		if v, _ := r.Fields[entry.FieldBluray3DRelease].(bool); !v {
			t.Errorf("entry %q missing bluray_3d_release=true (sibling BD3D in index)", r.Title)
		}
	}
}

func TestSearch_NegativeCache(t *testing.T) {
	sp := newSourceWithServer(t, "http://invalid.invalid", nil)
	sp.negCache.Set(indexKey("ObscureMovie"), time.Now())

	qe := entry.New("ObscureMovie", "")
	results, err := sp.Search(context.Background(), taskCtx(), qe)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("negative cache hit: got %d results, want 0", len(results))
	}
}

func TestSearch_FallsBackToSearchAndPopulates(t *testing.T) {
	body, err := os.ReadFile("../../../internal/bluray/testdata/search_avatar.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	sp := newSourceWithServer(t, srv.URL, nil)
	qe := entry.New("Avatar", "")
	results, err := sp.Search(context.Background(), taskCtx(), qe)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search fallback: got 0 results, expected several")
	}
	// Index was populated as a side effect.
	if hits, ok := sp.indexCache.Get(indexKey("Avatar")); !ok || len(hits) == 0 {
		t.Error("indexCache empty after /search/ fallback")
	}
}

func TestSearch_FallsBackThenCachesNegative(t *testing.T) {
	// Server returns search page with no .hoverlink results.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>no results</body></html>"))
	}))
	defer srv.Close()

	sp := newSourceWithServer(t, srv.URL, nil)
	qe := entry.New("DefinitelyDoesNotExist", "")
	results, err := sp.Search(context.Background(), taskCtx(), qe)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Search empty: got %d results, want 0", len(results))
	}
	if _, ok := sp.negCache.Get(indexKey("DefinitelyDoesNotExist")); !ok {
		t.Error("negCache not populated after empty search")
	}
}

func TestValidate_RejectsUnknownKeys(t *testing.T) {
	errs := validate(map[string]any{"bogus": "x", "months": int64(2)})
	var found bool
	for _, e := range errs {
		if e != nil && containsString(e.Error(), "bogus") {
			found = true
		}
	}
	if !found {
		t.Errorf("validate: expected unknown-key error for 'bogus', got %v", errs)
	}
}

func TestValidate_AcceptsKnownKeys(t *testing.T) {
	errs := validate(map[string]any{
		"country": "us", "months": int64(3),
		"cache_ttl": "720h", "request_interval": "1s",
	})
	if len(errs) != 0 {
		t.Errorf("validate: unexpected errors: %v", errs)
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
