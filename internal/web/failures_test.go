package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/failures"
	"github.com/brunoga/pipeliner/internal/store"
)

func newFailuresServer(t *testing.T) (*httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	srv.SetStore(db)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/failures", srv.apiFailures)
	return httptest.NewServer(mux), db
}

func TestApiFailures(t *testing.T) {
	hs, db := newFailuresServer(t)
	defer hs.Close()

	log := failures.New(db.Bucket(failures.BucketName))
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	log.Append(failures.Record{Title: "SNW S04E05 1080p", Reason: "deluge: connection refused",
		Node: "deluge_7", Task: "tv", FailedAt: base})
	log.Append(failures.Record{Title: "Silo S01E01", Reason: "exec: exit 1",
		Task: "tv", FailedAt: base.Add(time.Hour)})

	// Blank query → recent, newest first.
	resp := traceGet(t, hs.URL+"/api/failures")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Failures []failures.Record `json:"failures"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(out.Failures))
	}
	if out.Failures[0].Title != "Silo S01E01" {
		t.Errorf("expected newest (Silo) first, got %q", out.Failures[0].Title)
	}

	// Filtered query.
	resp2 := traceGet(t, hs.URL+"/api/failures?q=snw")
	defer resp2.Body.Close()
	var out2 struct {
		Failures []failures.Record `json:"failures"`
	}
	json.NewDecoder(resp2.Body).Decode(&out2)
	if len(out2.Failures) != 1 || out2.Failures[0].Node != "deluge_7" {
		t.Errorf("filtered query wrong: %+v", out2.Failures)
	}
}

func TestApiFailuresEmpty(t *testing.T) {
	hs, _ := newFailuresServer(t)
	defer hs.Close()
	resp := traceGet(t, hs.URL+"/api/failures")
	defer resp.Body.Close()
	var out struct {
		Failures []failures.Record `json:"failures"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Failures) != 0 {
		t.Errorf("expected no failures, got %d", len(out.Failures))
	}
}
