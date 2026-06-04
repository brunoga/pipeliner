package bluray

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newFixtureServer routes URL paths to fixture filenames. Unknown paths get 404.
func newFixtureServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=ISO-8859-1")
		w.Write(readFixture(t, fixture))
	}))
}

func TestClient_GetRelease(t *testing.T) {
	srv := newFixtureServer(t, map[string]string{
		"/movies/Avatar-3D-Blu-ray/26954/": "release_detail_avatar3d.html",
	})
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithRequestInterval(0))
	r, err := c.GetRelease(context.Background(), "26954", "Avatar-3D-Blu-ray")
	if err != nil {
		t.Fatalf("GetRelease: %v", err)
	}
	if r.ID != "26954" {
		t.Errorf("ID: got %q, want 26954", r.ID)
	}
	if r.Format != FormatBD3D {
		t.Errorf("Format: got %q, want BD3D", r.Format)
	}
	if r.Codec != "MPEG-4 MVC" {
		t.Errorf("Codec: got %q, want MPEG-4 MVC", r.Codec)
	}
}

func TestClient_GetRelease_NotFound(t *testing.T) {
	srv := newFixtureServer(t, nil)
	defer srv.Close()
	c := New(WithBaseURL(srv.URL), WithRequestInterval(0))
	if _, err := c.GetRelease(context.Background(), "999999", "Bogus"); err == nil {
		t.Fatal("GetRelease(unknown id): want error, got nil")
	}
}

func TestClient_GetRelease_EmptyID(t *testing.T) {
	c := New(WithRequestInterval(0))
	if _, err := c.GetRelease(context.Background(), "", ""); err == nil {
		t.Fatal("GetRelease(empty id): want error, got nil")
	}
}

func TestClient_ListMonth(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movies/releasedates.php" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		w.Write(readFixture(t, "calendar_2025_12.html"))
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithRequestInterval(0))
	rows, err := c.ListMonth(context.Background(), 2025, 12)
	if err != nil {
		t.Fatalf("ListMonth: %v", err)
	}
	if !strings.Contains(gotQuery, "year=2025") || !strings.Contains(gotQuery, "month=12") {
		t.Errorf("query: got %q, want year=2025 and month=12", gotQuery)
	}
	if len(rows) < 20 {
		t.Errorf("rows: got %d, want >= 20", len(rows))
	}
}

func TestClient_ListMonth_InvalidArgs(t *testing.T) {
	c := New(WithRequestInterval(0))
	for _, tc := range []struct{ y, m int }{{0, 1}, {2025, 0}, {2025, 13}} {
		if _, err := c.ListMonth(context.Background(), tc.y, tc.m); err == nil {
			t.Errorf("ListMonth(%d, %d): want error, got nil", tc.y, tc.m)
		}
	}
}

func TestClient_SearchTitle(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		w.Write(readFixture(t, "search_avatar.html"))
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithRequestInterval(0))
	results, err := c.SearchTitle(context.Background(), "Avatar", 0)
	if err != nil {
		t.Fatalf("SearchTitle: %v", err)
	}
	if !strings.Contains(gotQuery, "quicksearch=1") ||
		!strings.Contains(gotQuery, "section=bluraymovies") ||
		!strings.Contains(gotQuery, "quicksearch_keyword=Avatar") {
		t.Errorf("query missing expected params: %q", gotQuery)
	}
	if len(results) < 5 {
		t.Errorf("results: got %d, want >= 5", len(results))
	}
}

func TestClient_SearchTitle_YearFilter(t *testing.T) {
	srv := newFixtureServer(t, map[string]string{
		"/search/": "search_avatar.html",
	})
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithRequestInterval(0))
	results, err := c.SearchTitle(context.Background(), "Avatar", 2009)
	if err != nil {
		t.Fatalf("SearchTitle: %v", err)
	}
	for _, r := range results {
		if r.Year != 0 && r.Year != 2009 {
			t.Errorf("year filter leaked: %+v", r)
		}
	}
	// And the 2009 Avatar 3D release survives the filter.
	var found bool
	for _, r := range results {
		if r.ID == "26954" {
			found = true
		}
	}
	if !found {
		t.Error("year=2009 filter dropped the expected ID 26954")
	}
}

func TestClient_SearchTitle_EmptyTitle(t *testing.T) {
	c := New(WithRequestInterval(0))
	if _, err := c.SearchTitle(context.Background(), "  ", 0); err == nil {
		t.Fatal("SearchTitle(empty): want error, got nil")
	}
}

func TestClient_RateLimit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithRequestInterval(50*time.Millisecond))
	start := time.Now()
	for range 3 {
		_, _ = c.get(context.Background(), srv.URL+"/")
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("rate limit: 3 requests took %v, want >= 100ms", elapsed)
	}
	if hits != 3 {
		t.Errorf("hits: got %d, want 3", hits)
	}
}

func TestClient_RespectsCountryCookie(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("country"); err == nil {
			gotCookie = cookie.Value
		}
		w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCountry("uk"), WithRequestInterval(0))
	_, _ = c.get(context.Background(), srv.URL+"/")
	if gotCookie != "uk" {
		t.Errorf("country cookie: got %q, want uk", gotCookie)
	}
}

func TestClient_ContextCancellationDuringRateLimit(t *testing.T) {
	// Use a long gap so wait() blocks; cancel context partway through.
	c := New(WithRequestInterval(1 * time.Hour))
	c.next = time.Now().Add(1 * time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.wait(ctx); err == nil {
		t.Fatal("wait: want context-deadline error, got nil")
	}
}
