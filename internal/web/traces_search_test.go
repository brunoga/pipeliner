package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/traces"
)

func newTraceSearchServer(t *testing.T) (*httptest.Server, *traces.Store) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ts := traces.NewStore(db.Bucket(traces.BucketName))
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	srv.SetTraceStore(ts)
	mux := http.NewServeMux()
	// Register the wildcard route too, to prove "search" resolves to the
	// specific handler and not {task}="search".
	mux.HandleFunc("GET /api/traces/search", srv.apiTraceSearch)
	mux.HandleFunc("GET /api/traces/{task}", srv.apiTraceList)
	return httptest.NewServer(mux), ts
}

func TestApiTraceSearch(t *testing.T) {
	hs, ts := newTraceSearchServer(t)
	defer hs.Close()

	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	ts.Put(traces.RunTrace{RunID: "r1", Task: "tv", At: base, Entries: []executor.EntryTrace{
		{Title: "Star Trek Strange New Worlds S04E05 720p", Final: "rejected", Reason: "dedup"},
	}})
	ts.Put(traces.RunTrace{RunID: "r2", Task: "tv", At: base.Add(time.Hour), Entries: []executor.EntryTrace{
		{Title: "Star Trek Strange New Worlds S04E05 1080p", Final: "accepted"},
	}})

	resp := traceGet(t, hs.URL+"/api/traces/search?q=strange+new+worlds")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Occurrences []traces.Occurrence `json:"occurrences"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Occurrences) != 2 {
		t.Fatalf("expected 2 occurrences, got %d", len(out.Occurrences))
	}
	if out.Occurrences[0].RunID != "r2" {
		t.Errorf("expected newest (r2) first, got %s", out.Occurrences[0].RunID)
	}
	if out.Occurrences[0].Entry.Final != "accepted" {
		t.Errorf("occ[0] final = %q", out.Occurrences[0].Entry.Final)
	}
}

func TestApiTraceSearchEmptyQuery(t *testing.T) {
	hs, _ := newTraceSearchServer(t)
	defer hs.Close()
	resp := traceGet(t, hs.URL+"/api/traces/search?q=")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Occurrences []traces.Occurrence `json:"occurrences"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Occurrences) != 0 {
		t.Errorf("empty query should return no occurrences, got %d", len(out.Occurrences))
	}
}

func TestApiTraceSearchLimit(t *testing.T) {
	hs, ts := newTraceSearchServer(t)
	defer hs.Close()
	entries := []executor.EntryTrace{}
	for i := 0; i < 5; i++ {
		entries = append(entries, executor.EntryTrace{Title: "Common Show", Final: "accepted"})
	}
	ts.Put(traces.RunTrace{RunID: "r1", Task: "tv", At: time.Now(), Entries: entries})
	resp := traceGet(t, hs.URL+"/api/traces/search?q=common&limit=2")
	defer resp.Body.Close()
	var out struct {
		Occurrences []traces.Occurrence `json:"occurrences"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Occurrences) != 2 {
		t.Errorf("limit=2 not applied: got %d", len(out.Occurrences))
	}
}
