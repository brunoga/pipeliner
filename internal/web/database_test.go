package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/store"
)

// newDBTestServer builds a Server with an in-memory SQLite store and a mux
// exposing only the database API endpoints.
func newDBTestServer(t *testing.T) (*Server, *httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/db/buckets", srv.apiDBBuckets)
	mux.HandleFunc("GET /api/db/buckets/{name}", srv.apiDBGetBucket)
	mux.HandleFunc("DELETE /api/db/buckets/{name}", srv.apiDBClearBucket)
	mux.HandleFunc("DELETE /api/db/entries/{name}", srv.apiDBDeleteEntry)

	return srv, httptest.NewServer(mux), db
}

func deleteReq(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, url,
		bytes.NewReader(body))
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

// ── GET /api/db/buckets ───────────────────────────────────────────────────────

func TestDBBucketsEmpty(t *testing.T) {
	_, ts, _ := newDBTestServer(t)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Buckets []any `json:"buckets"`
	}
	json.NewDecoder(resp.Body).Decode(&out) //nolint:errcheck
	if len(out.Buckets) != 0 {
		t.Errorf("expected empty bucket list, got %d", len(out.Buckets))
	}
}

func TestDBBucketsListed(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	// Seed two buckets.
	db.Bucket("series").Put("key1", "val1")   //nolint:errcheck
	db.Bucket("cache_tvdb").Put("key2", "val2") //nolint:errcheck
	db.Bucket("cache_tvdb").Put("key3", "val3") //nolint:errcheck

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()

	var out struct {
		Buckets []struct {
			Name     string `json:"name"`
			Count    int    `json:"count"`
			Category string `json:"category"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Buckets) != 2 {
		t.Fatalf("want 2 buckets, got %d", len(out.Buckets))
	}
	byName := map[string]int{}
	byCategory := map[string]string{}
	for _, b := range out.Buckets {
		byName[b.Name] = b.Count
		byCategory[b.Name] = b.Category
	}
	if byName["series"] != 1 {
		t.Errorf("series count: got %d, want 1", byName["series"])
	}
	if byName["cache_tvdb"] != 2 {
		t.Errorf("cache_tvdb count: got %d, want 2", byName["cache_tvdb"])
	}
	if byCategory["series"] != "tracker" {
		t.Errorf("series category: got %q, want tracker", byCategory["series"])
	}
	if byCategory["cache_tvdb"] != "cache" {
		t.Errorf("cache_tvdb category: got %q, want cache", byCategory["cache_tvdb"])
	}
}

// ── GET /api/db/buckets/{name} ────────────────────────────────────────────────

func TestDBGetBucketEntries(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("mylist").Put("foo", "bar")  //nolint:errcheck
	db.Bucket("mylist").Put("baz", "qux")  //nolint:errcheck

	resp := get(t, ts.URL+"/api/db/buckets/mylist")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Entries []struct {
			Key string `json:"key"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Errorf("want 2 entries, got %d", len(out.Entries))
	}
}

func TestDBGetSeriesBucketGrouped(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	// Write two series tracker records.
	type rec struct {
		SeriesName   string `json:"series_name"`
		EpisodeID    string `json:"episode_id"`
		DownloadedAt string `json:"downloaded_at"`
		Quality      struct {
			Str string `json:"string"`
		} `json:"quality"`
	}
	db.Bucket("series").Put("Breaking Bad|S01E01", rec{SeriesName: "Breaking Bad", EpisodeID: "S01E01"})             //nolint:errcheck
	db.Bucket("series").Put("Breaking Bad|S01E02", rec{SeriesName: "Breaking Bad", EpisodeID: "S01E02"})             //nolint:errcheck
	db.Bucket("series").Put("Dark|S01E01", rec{SeriesName: "Dark", EpisodeID: "S01E01"}) //nolint:errcheck

	resp := get(t, ts.URL+"/api/db/buckets/series")
	defer resp.Body.Close()

	var out struct {
		Grouped []struct {
			Name     string `json:"name"`
			Episodes []any  `json:"episodes"`
		} `json:"grouped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Grouped) != 2 {
		t.Fatalf("want 2 shows, got %d", len(out.Grouped))
	}
	// Grouped is sorted alphabetically: Breaking Bad, Dark.
	if out.Grouped[0].Name != "Breaking Bad" {
		t.Errorf("first show: got %q, want Breaking Bad", out.Grouped[0].Name)
	}
	if len(out.Grouped[0].Episodes) != 2 {
		t.Errorf("Breaking Bad episodes: got %d, want 2", len(out.Grouped[0].Episodes))
	}
}

// ── DELETE /api/db/buckets/{name} ─────────────────────────────────────────────

func TestDBClearBucket(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("cache_tvdb").Put("k1", "v1") //nolint:errcheck
	db.Bucket("cache_tvdb").Put("k2", "v2") //nolint:errcheck

	resp := deleteReq(t, ts.URL+"/api/db/buckets/cache_tvdb", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	keys, _ := db.Bucket("cache_tvdb").Keys()
	if len(keys) != 0 {
		t.Errorf("bucket should be empty after clear, got %d keys", len(keys))
	}
}

// ── DELETE /api/db/entries/{name} ─────────────────────────────────────────────

func TestDBDeleteEntry(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("series").Put("Breaking Bad|S01E01", "rec1") //nolint:errcheck
	db.Bucket("series").Put("Breaking Bad|S01E02", "rec2") //nolint:errcheck

	body, _ := json.Marshal(map[string]string{"key": "Breaking Bad|S01E01"})
	resp := deleteReq(t, ts.URL+"/api/db/entries/series", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	keys, _ := db.Bucket("series").Keys()
	if len(keys) != 1 || keys[0] != "Breaking Bad|S01E02" {
		t.Errorf("expected only S01E02 remaining, got %v", keys)
	}
}

func TestDBDeleteEntryMissingKey(t *testing.T) {
	_, ts, _ := newDBTestServer(t)
	defer ts.Close()

	resp := deleteReq(t, ts.URL+"/api/db/entries/series", []byte(`{}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing key, got %d", resp.StatusCode)
	}
}

func TestDBNotAvailableWithoutStore(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/db/buckets", srv.apiDBBuckets)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("expected 501 when store not set, got %d", resp.StatusCode)
	}
}
