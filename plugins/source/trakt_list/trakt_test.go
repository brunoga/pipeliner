package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	itrakt "github.com/brunoga/pipeliner/internal/trakt"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
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

func TestCacheKeyIncludesTypeAndList(t *testing.T) {
	cases := []struct {
		itemType string
		list     string
		want     string
	}{
		{"shows", "watchlist", "trakt_list:shows:watchlist"},
		{"movies", "watchlist", "trakt_list:movies:watchlist"},
		{"shows", "ratings", "trakt_list:shows:ratings"},
		{"movies", "collection", "trakt_list:movies:collection"},
	}
	for _, tc := range cases {
		p := &traktInputPlugin{itemType: tc.itemType, list: tc.list}
		if got := p.CacheKey(); got != tc.want {
			t.Errorf("CacheKey(%s,%s): got %q, want %q", tc.itemType, tc.list, got, tc.want)
		}
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("trakt_list")
	if !ok {
		t.Fatal("trakt_list not registered")
	}
	if d.Role != plugin.RoleSource {
		t.Errorf("phase: got %v", d.Role)
	}
}

func TestClientSecretUsesStoredToken(t *testing.T) {
	// When client_secret is set, the plugin should read the token from the DB.
	srv := makeServer(t, []map[string]any{
		{"title": "Avatar", "year": 2009, "ids": map[string]any{"trakt": 1, "slug": "avatar-2009"}},
	})
	orig := itrakt.BaseURL
	itrakt.BaseURL = srv.URL
	t.Cleanup(func() { itrakt.BaseURL = orig })

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Pre-store a token.
	tok := &itrakt.Token{
		AccessToken:  "stored-token",
		RefreshToken: "stored-ref",
		ExpiresIn:    7776000,
		CreatedAt:    time.Now().Unix(),
	}
	if err := itrakt.SaveToken(db.Bucket(itrakt.AuthBucket), "my-client-id", tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	p, err := newPlugin(map[string]any{
		"client_id":     "my-client-id",
		"client_secret": "my-secret",
		"type":          "movies",
		"list":          "trending",
	}, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}

	entries, err := p.(*traktInputPlugin).Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 1 || entries[0].Title != "Avatar" {
		t.Errorf("unexpected entries: %v", entries)
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

	entries, err := p.(*traktInputPlugin).Generate(context.Background(), nil)
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
