package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func newTrackerTestServer(t *testing.T) (*httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/db/series/mark", srv.apiDBMarkSeries)
	mux.HandleFunc("POST /api/db/movies/mark", srv.apiDBMarkMovie)
	return httptest.NewServer(mux), db
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestApiMarkSeriesStoresNormalizedKey(t *testing.T) {
	ts, db := newTrackerTestServer(t)
	defer ts.Close()

	resp := postJSON(t, ts.URL+"/api/db/series/mark", map[string]any{
		"show":       "Star Trek: Strange New Worlds",
		"episode_id": "s4e5",
		"quality":    "1080p web h264",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Key     string `json:"key"`
		Existed bool   `json:"existed"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Key != "star trek strange new worlds|S04E05" {
		t.Errorf("key = %q", out.Key)
	}
	if out.Existed {
		t.Errorf("existed should be false for a new record")
	}
	// The record must be looked up under the normalized key + canonical epid.
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if !tr.IsSeen("star trek strange new worlds", "S04E05") {
		t.Errorf("record not stored under the normalized/canonical key")
	}
}

func TestApiMarkSeriesExistedFlag(t *testing.T) {
	ts, _ := newTrackerTestServer(t)
	defer ts.Close()

	body := map[string]any{"show": "Silo", "episode_id": "S02E01"}
	postJSON(t, ts.URL+"/api/db/series/mark", body).Body.Close()
	resp := postJSON(t, ts.URL+"/api/db/series/mark", body)
	defer resp.Body.Close()
	var out struct {
		Existed bool `json:"existed"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if !out.Existed {
		t.Errorf("second mark should report existed=true")
	}
}

func TestApiMarkSeriesBadEpisodeID(t *testing.T) {
	ts, _ := newTrackerTestServer(t)
	defer ts.Close()
	resp := postJSON(t, ts.URL+"/api/db/series/mark", map[string]any{"show": "Silo", "episode_id": "garbage"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad episode_id, got %d", resp.StatusCode)
	}
}

func TestApiMarkSeriesMissingShow(t *testing.T) {
	ts, _ := newTrackerTestServer(t)
	defer ts.Close()
	resp := postJSON(t, ts.URL+"/api/db/series/mark", map[string]any{"episode_id": "S01E01"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing show, got %d", resp.StatusCode)
	}
}

func TestApiMarkMovie(t *testing.T) {
	ts, db := newTrackerTestServer(t)
	defer ts.Close()

	resp := postJSON(t, ts.URL+"/api/db/movies/mark", map[string]any{
		"title":   "Furiosa: A Mad Max Saga",
		"year":    2024,
		"quality": "1080p bluray",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	tr := movies.NewTracker(db.Bucket(movies.TrackerBucketName))
	if !tr.IsSeen("furiosa a mad max saga", 2024, false) {
		t.Errorf("movie not stored under normalized title")
	}
}

func TestApiMarkMovieMissingTitle(t *testing.T) {
	ts, _ := newTrackerTestServer(t)
	defer ts.Close()
	resp := postJSON(t, ts.URL+"/api/db/movies/mark", map[string]any{"year": 2024})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing title, got %d", resp.StatusCode)
	}
}
