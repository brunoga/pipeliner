package lifecycle

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

// fixedNow is the reference "now" for aired-date comparisons in all tests.
var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// mockTVDB spins up a TVDB v4 API stub. status is returned for the show;
// episodes is the official episode list. failEpisodes makes the episode
// endpoint return HTTP 500.
func mockTVDB(t *testing.T, status string, episodes []map[string]any, failEpisodes bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "test-jwt"}, "status": "success",
			})
		case "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"tvdb_id": "81189", "name": "My Show", "status": status},
				},
				"status": "success",
			})
		case "/v4/series/81189":
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"name": "My Show", "status": map[string]any{"id": 2, "name": status},
				},
				"status": "success",
			})
		case "/v4/series/81189/episodes/official":
			if failEpisodes {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]any{"episodes": episodes},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// openPlugin builds a lifecyclePlugin against the mock server with a fixed
// clock, returning the plugin and its store for tracker seeding.
func openPlugin(t *testing.T, srv *httptest.Server, extra map[string]any) (*lifecyclePlugin, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := map[string]any{"api_key": "test-key"}
	maps.Copy(cfg, extra)
	pl, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p := pl.(*lifecyclePlugin)
	p.client.BaseURL = srv.URL + "/v4"
	p.now = func() time.Time { return fixedNow }
	return p, db
}

func seedTracker(t *testing.T, db *store.SQLiteStore, name string, episodeIDs ...string) {
	t.Helper()
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	for _, id := range episodeIDs {
		if err := tr.Mark(series.Record{SeriesName: name, EpisodeID: id, DownloadedAt: fixedNow}); err != nil {
			t.Fatalf("Mark: %v", err)
		}
	}
}

func showEntry() *entry.Entry {
	e := entry.New("My Show", "pipeliner://series/my%20show")
	e.Set(entry.FieldSeriesName, "my show")
	return e
}

func classifyOne(t *testing.T, p *lifecyclePlugin, e *entry.Entry) string {
	t.Helper()
	out, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Process returned %d entries, want 1", len(out))
	}
	return e.GetString(entry.FieldSeriesLifecycle)
}

// twoAired is a standard two-episode aired list plus one unaired future
// episode and one aired special.
var twoAired = []map[string]any{
	{"id": 1, "seasonNumber": 1, "number": 1, "aired": "2008-01-20"},
	{"id": 2, "seasonNumber": 1, "number": 2, "aired": "2008-01-27"},
	{"id": 3, "seasonNumber": 1, "number": 3, "aired": "2030-06-01"}, // future: not aired yet
	{"id": 4, "seasonNumber": 0, "number": 1, "aired": "2008-05-01"}, // special
}

func TestContinuingShowIsActive(t *testing.T) {
	srv := mockTVDB(t, "Continuing", twoAired, false)
	p, db := openPlugin(t, srv, nil)
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	e := showEntry()
	if got := classifyOne(t, p, e); got != entry.SeriesLifecycleActive {
		t.Errorf("lifecycle: got %q, want active", got)
	}
	if got := e.GetString(entry.FieldSeriesStatus); got != "Continuing" {
		t.Errorf("series_status: got %q", got)
	}
	if got := e.GetString("tvdb_id"); got != "81189" {
		t.Errorf("tvdb_id: got %q", got)
	}
	// Active shows never fetch/diff episodes.
	if _, ok := e.Get(entry.FieldSeriesAiredEpisodeCount); ok {
		t.Error("aired count should not be set for active shows")
	}
}

func TestEndedFullyTrackedIsComplete(t *testing.T) {
	srv := mockTVDB(t, "Ended", twoAired, false)
	p, db := openPlugin(t, srv, nil)
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	e := showEntry()
	if got := classifyOne(t, p, e); got != entry.SeriesLifecycleComplete {
		t.Errorf("lifecycle: got %q, want complete", got)
	}
	// The future episode and the special must not count as aired.
	if got := e.GetInt(entry.FieldSeriesAiredEpisodeCount); got != 2 {
		t.Errorf("aired count: got %d, want 2", got)
	}
	if got := e.GetInt(entry.FieldSeriesMissingEpisodeCount); got != 0 {
		t.Errorf("missing count: got %d, want 0", got)
	}
}

func TestEndedWithGapsIsDormant(t *testing.T) {
	srv := mockTVDB(t, "Ended", twoAired, false)
	p, db := openPlugin(t, srv, nil)
	seedTracker(t, db, "my show", "S01E01") // S01E02 missing

	e := showEntry()
	if got := classifyOne(t, p, e); got != entry.SeriesLifecycleDormant {
		t.Errorf("lifecycle: got %q, want dormant", got)
	}
	if got := e.GetInt(entry.FieldSeriesMissingEpisodeCount); got != 1 {
		t.Errorf("missing count: got %d, want 1", got)
	}
}

func TestCancelledCountsAsEnded(t *testing.T) {
	srv := mockTVDB(t, "Cancelled", twoAired, false)
	p, db := openPlugin(t, srv, nil)
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	if got := classifyOne(t, p, showEntry()); got != entry.SeriesLifecycleComplete {
		t.Errorf("lifecycle: got %q, want complete", got)
	}
}

func TestSpecialsExcludedByDefaultIncludedOnOptIn(t *testing.T) {
	// Tracker has both regular episodes but not the special.
	srv := mockTVDB(t, "Ended", twoAired, false)
	p, db := openPlugin(t, srv, nil)
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	if got := classifyOne(t, p, showEntry()); got != entry.SeriesLifecycleComplete {
		t.Errorf("default (specials excluded): got %q, want complete", got)
	}

	// Same data with include_specials=true: the untracked S00E01 is a gap.
	srv2 := mockTVDB(t, "Ended", twoAired, false)
	p2, db2 := openPlugin(t, srv2, map[string]any{"include_specials": true})
	seedTracker(t, db2, "my show", "S01E01", "S01E02")

	e := showEntry()
	if got := classifyOne(t, p2, e); got != entry.SeriesLifecycleDormant {
		t.Errorf("include_specials: got %q, want dormant", got)
	}
	if got := e.GetInt(entry.FieldSeriesAiredEpisodeCount); got != 3 {
		t.Errorf("aired count with specials: got %d, want 3", got)
	}
}

func TestEpisodesWithoutAirDateDoNotCount(t *testing.T) {
	eps := []map[string]any{
		{"id": 1, "seasonNumber": 1, "number": 1, "aired": "2008-01-20"},
		{"id": 2, "seasonNumber": 1, "number": 2}, // no air date: unscheduled
	}
	srv := mockTVDB(t, "Ended", eps, false)
	p, db := openPlugin(t, srv, nil)
	seedTracker(t, db, "my show", "S01E01")

	if got := classifyOne(t, p, showEntry()); got != entry.SeriesLifecycleComplete {
		t.Errorf("lifecycle: got %q, want complete (undated episode ignored)", got)
	}
}

func TestLookupFailureClassifiesActive(t *testing.T) {
	// Server with no routes at all: search fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/login" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "t"}, "status": "success",
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	p, _ := openPlugin(t, srv, nil)

	e := showEntry()
	if got := classifyOne(t, p, e); got != entry.SeriesLifecycleActive {
		t.Errorf("lifecycle on lookup failure: got %q, want active", got)
	}
	if _, ok := e.Get(entry.FieldSeriesStatus); ok {
		t.Error("series_status must not be set when the lookup failed")
	}
}

func TestEndedButEpisodeFetchFailsClassifiesActive(t *testing.T) {
	srv := mockTVDB(t, "Ended", nil, true)
	p, db := openPlugin(t, srv, nil)
	seedTracker(t, db, "my show", "S01E01")

	e := showEntry()
	if got := classifyOne(t, p, e); got != entry.SeriesLifecycleActive {
		t.Errorf("lifecycle: got %q, want active (unverifiable)", got)
	}
	if got := e.GetString(entry.FieldSeriesStatus); got != "Ended" {
		t.Errorf("series_status should still be set, got %q", got)
	}
}

func TestResolveByExistingTvdbID(t *testing.T) {
	var searched bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "t"}, "status": "success",
			})
		case "/v4/search":
			searched = true
			http.NotFound(w, r)
		case "/v4/series/81189":
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]any{"name": "My Show", "status": map[string]any{"name": "Continuing"}},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	p, _ := openPlugin(t, srv, nil)

	e := showEntry()
	e.Set("tvdb_id", "81189")
	if got := classifyOne(t, p, e); got != entry.SeriesLifecycleActive {
		t.Errorf("lifecycle: got %q, want active", got)
	}
	if searched {
		t.Error("search endpoint must not be hit when tvdb_id is present")
	}
}

func TestTitleFallbackWhenNoSeriesName(t *testing.T) {
	srv := mockTVDB(t, "Ended", twoAired, false)
	p, db := openPlugin(t, srv, nil)
	// Tracker keyed by the normalized title.
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	e := entry.New("My.Show", "http://x/1") // no series_name field
	if got := classifyOne(t, p, e); got != entry.SeriesLifecycleComplete {
		t.Errorf("lifecycle via normalized title: got %q, want complete", got)
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Error("missing api_key should fail validation")
	}
	if errs := validate(map[string]any{"api_key": "k", "cache_ttl": "nope"}); len(errs) == 0 {
		t.Error("bad cache_ttl should fail validation")
	}
	if errs := validate(map[string]any{"api_key": "k", "bogus": 1}); len(errs) == 0 {
		t.Error("unknown key should fail validation")
	}
	if errs := validate(map[string]any{"api_key": "k", "cache_ttl": "1h", "include_specials": true}); len(errs) != 0 {
		t.Errorf("valid config should pass, got %v", errs)
	}
}
