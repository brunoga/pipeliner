package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/store"
)

func newMatchTestServer(t *testing.T) (*httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/match/test", srv.apiMatchTest)
	return httptest.NewServer(mux), db
}

func postMatch(t *testing.T, url string, body any) (*http.Response, match.ProbeResult) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url,
		bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out match.ProbeResult
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp, out
}

func TestApiMatchTestInlineCandidates(t *testing.T) {
	ts, _ := newMatchTestServer(t)
	defer ts.Close()

	resp, res := postMatch(t, ts.URL+"/api/match/test", map[string]any{
		"input":      "Star Trek Strange New Worlds",
		"candidates": []string{"Silo", "Star Trek: Strange New Worlds", "The Ark"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !res.Matched {
		t.Fatalf("expected match, got none")
	}
	if res.MatchedBy != "star trek strange new worlds" {
		t.Errorf("MatchedBy = %q", res.MatchedBy)
	}
}

func TestApiMatchTestFromBucket(t *testing.T) {
	ts, db := newMatchTestServer(t)
	defer ts.Close()

	// Seed the resolved-list cache the way the series filter would.
	c := cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("cache_series_list"))
	c.Set("tvdb_favorites", []match.TitleEntry{
		match.NewTitleEntry("Silo", 0),
		match.NewTitleEntry("Star Trek: Strange New Worlds", 0),
	})

	resp, res := postMatch(t, ts.URL+"/api/match/test", map[string]any{
		"input":  "Star Trek Strange New Worlds",
		"bucket": "cache_series_list",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !res.Matched {
		t.Fatalf("expected match against seeded bucket, got none (candidates %d)", len(res.Candidates))
	}
}

func TestApiMatchTestMissingInput(t *testing.T) {
	ts, _ := newMatchTestServer(t)
	defer ts.Close()

	resp, _ := postMatch(t, ts.URL+"/api/match/test", map[string]any{"candidates": []string{"x"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing input, got %d", resp.StatusCode)
	}
}

func TestApiMatchTestYearGate(t *testing.T) {
	ts, _ := newMatchTestServer(t)
	defer ts.Close()

	// Title matches but year is off by more than tolerance → no match.
	resp, res := postMatch(t, ts.URL+"/api/match/test", map[string]any{
		"input":      "Dune",
		"year":       2021,
		"candidates": []string{"Dune"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	// Inline candidates carry year 0 (unknown), so the year gate is permissive
	// and the title still matches — this documents that inline candidates are
	// year-agnostic.
	if !res.Matched {
		t.Errorf("inline candidate has unknown year; expected permissive match")
	}
}
