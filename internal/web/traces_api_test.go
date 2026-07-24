package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/traces"
)

func traceGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestTraceEndpoints(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ts := traces.NewStore(db.Bucket(traces.BucketName))
	if err := ts.Put(traces.RunTrace{RunID: "r1", Task: "tv", At: time.Now(),
		Entries: []executor.EntryTrace{{Title: "e", URL: "u", Final: "rejected", Reason: "why"}}}); err != nil {
		t.Fatal(err)
	}

	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	srv.SetTraceStore(ts)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/traces/{task}", srv.apiTraceList)
	mux.HandleFunc("GET /api/traces/{task}/{run}", srv.apiTraceGet)
	hs := httptest.NewServer(mux)
	defer hs.Close()

	resp := traceGet(t, hs.URL+"/api/traces/tv")
	defer resp.Body.Close()
	var list struct {
		Runs []traces.Meta `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil || len(list.Runs) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}

	resp2 := traceGet(t, hs.URL+"/api/traces/tv/r1")
	defer resp2.Body.Close()
	var rt traces.RunTrace
	if err := json.NewDecoder(resp2.Body).Decode(&rt); err != nil || len(rt.Entries) != 1 || rt.Entries[0].Reason != "why" {
		t.Fatalf("get: %+v err=%v", rt, err)
	}

	resp3 := traceGet(t, hs.URL+"/api/traces/tv/missing")
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("missing run: want 404, got %d", resp3.StatusCode)
	}

	// Unwired store answers 501.
	bare := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux2 := http.NewServeMux()
	mux2.HandleFunc("GET /api/traces/{task}", bare.apiTraceList)
	hs2 := httptest.NewServer(mux2)
	defer hs2.Close()
	resp4 := traceGet(t, hs2.URL+"/api/traces/tv")
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unwired: want 501, got %d", resp4.StatusCode)
	}
}
