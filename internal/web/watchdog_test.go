package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/traces"
	"github.com/brunoga/pipeliner/internal/watchdog"
)

func newWatchdogServer(t *testing.T) (*httptest.Server, *store.SQLiteStore, *traces.Store) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ts := traces.NewStore(db.Bucket(traces.BucketName))
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	srv.SetStore(db)
	srv.SetTraceStore(ts)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/watchdog/stuck", srv.apiWatchdogStuck)
	return httptest.NewServer(mux), db, ts
}

func TestApiWatchdogStuck(t *testing.T) {
	hs, db, ts := newWatchdogServer(t)
	defer hs.Close()

	// Seed favorites (as the series filter would cache them).
	cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("cache_series_list")).
		Set("tvdb_favorites", []match.TitleEntry{
			match.NewTitleEntry("Silo", 0),
			match.NewTitleEntry("Star Trek: Strange New Worlds", 0),
		})

	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		ts.Put(traces.RunTrace{RunID: "r" + string(rune('1'+i)), Task: "tv", At: base.Add(time.Duration(i) * time.Hour),
			Entries: []executor.EntryTrace{
				{Title: "Star Trek Strange New Worlds S04E05 1080p WEB", Final: "rejected", Reason: "series: show not in list"},
			}})
	}

	resp := traceGet(t, hs.URL+"/api/watchdog/stuck")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Stuck   []watchdog.StuckFavorite `json:"stuck"`
		MinRuns int                      `json:"min_runs"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Stuck) != 1 {
		t.Fatalf("expected 1 stuck favorite, got %d", len(out.Stuck))
	}
	if out.Stuck[0].Favorite != "star trek strange new worlds" {
		t.Errorf("favorite = %q", out.Stuck[0].Favorite)
	}
	if out.MinRuns != watchdog.DefaultMinRuns {
		t.Errorf("min_runs default = %d", out.MinRuns)
	}
}

func TestApiWatchdogStuckMinRunsParam(t *testing.T) {
	hs, db, ts := newWatchdogServer(t)
	defer hs.Close()
	cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("cache_series_list")).
		Set("f", []match.TitleEntry{match.NewTitleEntry("Silo", 0)})
	base := time.Now()
	for i := 0; i < 2; i++ {
		ts.Put(traces.RunTrace{RunID: "r" + string(rune('1'+i)), Task: "tv", At: base,
			Entries: []executor.EntryTrace{{Title: "Silo S01E01 480p", Final: "rejected", Reason: "quality"}}})
	}
	// Default min_runs (3) → not stuck; min_runs=2 → stuck.
	resp := traceGet(t, hs.URL+"/api/watchdog/stuck?min_runs=2")
	defer resp.Body.Close()
	var out struct {
		Stuck []watchdog.StuckFavorite `json:"stuck"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Stuck) != 1 {
		t.Errorf("min_runs=2 should flag Silo, got %d", len(out.Stuck))
	}
}

func TestApiWatchdogStuckEmpty(t *testing.T) {
	hs, _, _ := newWatchdogServer(t)
	defer hs.Close()
	resp := traceGet(t, hs.URL+"/api/watchdog/stuck")
	defer resp.Body.Close()
	var out struct {
		Stuck []watchdog.StuckFavorite `json:"stuck"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Stuck) != 0 {
		t.Errorf("no favorites/traces should yield no stuck, got %d", len(out.Stuck))
	}
}
