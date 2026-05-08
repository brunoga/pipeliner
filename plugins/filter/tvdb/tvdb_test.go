package tvdb

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func tc() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

// mockTVDB serves:
//   - POST /login → JWT
//   - GET /user/favorites → series IDs
//   - GET /series/{id} → series name
type mockTVDB struct {
	favorites []int
	series    map[int]string // id → name
	loginCalls atomic.Int32
	favCalls   atomic.Int32
}

func (m *mockTVDB) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/login":
		m.loginCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"data":   map[string]string{"token": "test-token"},
			"status": "success",
		})
	case r.Method == http.MethodGet && r.URL.Path == "/user/favorites":
		m.favCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"data":   map[string]any{"series": m.favorites},
			"status": "success",
		})
	default:
		// GET /series/{id}
		var id int
		if n, _ := fmt.Sscanf(r.URL.Path, "/series/%d", &id); n == 1 {
			if name, ok := m.series[id]; ok {
				json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
					"data":   map[string]any{"id": id, "name": name, "tvdb_id": fmt.Sprintf("%d", id)},
					"status": "success",
				})
				return
			}
		}
		http.NotFound(w, r)
	}
}

func newMockServer(t *testing.T, favorites []int, series map[int]string) (*httptest.Server, *mockTVDB) {
	t.Helper()
	m := &mockTVDB{favorites: favorites, series: series}
	srv := httptest.NewServer(http.HandlerFunc(m.handler))
	return srv, m
}

func makeFilter(t *testing.T, srv *httptest.Server, extra map[string]any) *tvdbFilter {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := map[string]any{
		"api_key":  "test-key",
		"user_pin": "test-pin",
	}
	maps.Copy(cfg, extra)
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	f := p.(*tvdbFilter)
	f.client.BaseURL = srv.URL
	return f
}

func TestFilterAcceptsFavoriteShow(t *testing.T) {
	srv, _ := newMockServer(t, []int{1}, map[int]string{1: "Breaking Bad"})
	defer srv.Close()

	p := makeFilter(t, srv, nil)
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1")

	if err := p.Filter(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Error("favorited show should be accepted")
	}
}

func TestFilterLeavesNonFavoriteUndecided(t *testing.T) {
	srv, _ := newMockServer(t, []int{1}, map[int]string{1: "Breaking Bad"})
	defer srv.Close()

	p := makeFilter(t, srv, nil)
	e := entry.New("The.Wire.S01E01.720p.HDTV", "http://example.com/1")

	p.Filter(context.Background(), tc(), e) //nolint:errcheck
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("non-favorited show should be undecided, got accepted=%v rejected=%v", e.IsAccepted(), e.IsRejected())
	}
}

func TestFilterIgnoresNonEpisodeTitle(t *testing.T) {
	srv, _ := newMockServer(t, []int{1}, map[int]string{1: "Breaking Bad"})
	defer srv.Close()

	p := makeFilter(t, srv, nil)
	e := entry.New("Some Movie 2023 1080p BluRay", "http://example.com/1")

	if err := p.Filter(context.Background(), tc(), e); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if e.IsAccepted() || e.IsRejected() {
		t.Error("non-episode title should be left undecided")
	}
}

func TestFilterCachesWithinTTL(t *testing.T) {
	srv, mock := newMockServer(t, []int{1}, map[int]string{1: "Breaking Bad"})
	defer srv.Close()

	p := makeFilter(t, srv, map[string]any{"ttl": "1h"})

	e1 := entry.New("Breaking.Bad.S01E01.720p", "http://example.com/1")
	e2 := entry.New("Breaking.Bad.S01E02.720p", "http://example.com/2")
	p.Filter(context.Background(), tc(), e1) //nolint:errcheck
	p.Filter(context.Background(), tc(), e2) //nolint:errcheck

	if n := mock.favCalls.Load(); n != 1 {
		t.Errorf("favorites fetched %d times within TTL, want 1", n)
	}
}

func TestFilterRefreshesAfterTTL(t *testing.T) {
	srv, mock := newMockServer(t, []int{1}, map[int]string{1: "Breaking Bad"})
	defer srv.Close()

	p := makeFilter(t, srv, map[string]any{"ttl": "1ms"})

	e1 := entry.New("Breaking.Bad.S01E01.720p", "http://example.com/1")
	p.Filter(context.Background(), tc(), e1) //nolint:errcheck
	time.Sleep(5 * time.Millisecond)

	e2 := entry.New("Breaking.Bad.S01E02.720p", "http://example.com/2")
	p.Filter(context.Background(), tc(), e2) //nolint:errcheck

	if n := mock.favCalls.Load(); n < 2 {
		t.Errorf("favorites fetched %d times, want ≥2 after TTL expiry", n)
	}
}

func TestMissingAPIKey(t *testing.T) {
	_, err := newPlugin(map[string]any{"user_pin": "pin"}, nil)
	if err == nil {
		t.Error("expected error for missing api_key")
	}
}

func TestMissingUserPin(t *testing.T) {
	_, err := newPlugin(map[string]any{"api_key": "key"}, nil)
	if err == nil {
		t.Error("expected error for missing user_pin")
	}
}

func TestInvalidTTL(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"api_key":  "key",
		"user_pin": "pin",
		"ttl":      "not-a-duration",
	}, nil)
	if err == nil {
		t.Error("expected error for invalid ttl")
	}
}

func TestEmptyFavoritesNotCached(t *testing.T) {
	// A server with no favorites should not poison the cache — both calls must
	// hit the API rather than the second getting a cached empty slice.
	srv, mock := newMockServer(t, []int{}, map[int]string{})
	defer srv.Close()

	p := makeFilter(t, srv, map[string]any{"ttl": "1h"})

	e1 := entry.New("Breaking.Bad.S01E01.720p", "http://x.com/1")
	e2 := entry.New("Breaking.Bad.S01E02.720p", "http://x.com/2")
	p.Filter(context.Background(), tc(), e1) //nolint:errcheck
	p.Filter(context.Background(), tc(), e2) //nolint:errcheck

	if n := mock.favCalls.Load(); n < 2 {
		t.Errorf("empty favorites should not be cached; API called %d times, want ≥2", n)
	}
}

func TestPluginRegistered(t *testing.T) {
	if _, ok := plugin.Lookup("tvdb"); !ok {
		t.Error("tvdb plugin not registered")
	}
}
