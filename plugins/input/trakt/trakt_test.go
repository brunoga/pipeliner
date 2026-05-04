package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	itrakt "github.com/brunoga/pipeliner/internal/trakt"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeServer(t *testing.T, movies []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wrap each movie as {type: "movie", movie: {...}} like the real API
		out := make([]map[string]any, len(movies))
		for i, m := range movies {
			out[i] = map[string]any{"type": "movie", "movie": m}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMissingClientID(t *testing.T) {
	_, err := newPlugin(map[string]any{"type": "movies"}, nil)
	if err == nil {
		t.Fatal("expected error for missing client_id")
	}
}

func TestMissingType(t *testing.T) {
	_, err := newPlugin(map[string]any{"client_id": "id"}, nil)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestInvalidType(t *testing.T) {
	_, err := newPlugin(map[string]any{"client_id": "id", "type": "songs"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestDefaultList(t *testing.T) {
	p, err := newPlugin(map[string]any{"client_id": "id", "type": "movies"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	if p.(*traktInputPlugin).list != "watchlist" {
		t.Error("default list should be watchlist")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("input_trakt")
	if !ok {
		t.Fatal("input_trakt not registered")
	}
	if d.PluginPhase != plugin.PhaseInput {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}

func TestRunReturnsEntries(t *testing.T) {
	srv := makeServer(t, []map[string]any{
		{"title": "Inception", "year": 2010, "ids": map[string]any{"trakt": 1, "slug": "inception-2010", "imdb": "tt1375666", "tmdb": 27205}},
		{"title": "Interstellar", "year": 2014, "ids": map[string]any{"trakt": 2, "slug": "interstellar-2014"}},
	})

	orig := itrakt.BaseURL
	itrakt.BaseURL = srv.URL
	t.Cleanup(func() { itrakt.BaseURL = orig })

	p, err := newPlugin(map[string]any{
		"client_id":    "test",
		"type":         "movies",
		"list":         "trending",
	}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}

	entries, err := p.(*traktInputPlugin).Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Title != "Inception" {
		t.Errorf("first entry title: got %q", entries[0].Title)
	}
	const wantURL = "https://trakt.tv/movies/inception-2010"
	if entries[0].URL != wantURL {
		t.Errorf("URL: got %q, want %q", entries[0].URL, wantURL)
	}
	if v, _ := entries[0].Get("trakt_imdb_id"); v != "tt1375666" {
		t.Errorf("trakt_imdb_id: got %v", v)
	}
}
