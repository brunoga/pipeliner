package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

func makeServer(t *testing.T, movies []map[string]any) *httptest.Server {
	return makeServerForType(t, "movie", movies)
}

func makeServerForType(t *testing.T, itemType string, items []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wrap each item as {type: <itemType>, <itemType>: {...}} like the real API.
		out := make([]map[string]any, len(items))
		for i, m := range items {
			out[i] = map[string]any{"type": itemType, itemType: m}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
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
	if p.(*traktSourcePlugin).list != "watchlist" {
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
		p := &traktSourcePlugin{itemType: tc.itemType, list: tc.list}
		if got := p.CacheKey(); got != tc.want {
			t.Errorf("CacheKey(%s,%s): got %q, want %q", tc.itemType, tc.list, got, tc.want)
		}
	}
}

func TestHistoryAndRecommendationsAccepted(t *testing.T) {
	// Verify that history and recommendations are valid list values and hit
	// the correct private endpoints (both require a token).
	for _, list := range []string{"history", "recommendations"} {
		t.Run(list, func(t *testing.T) {
			srv := makeServer(t, []map[string]any{
				{"title": "Dune", "year": 2021, "ids": map[string]any{"trakt": 1, "slug": "dune-2021"}},
			})
			orig := itrakt.BaseURL
			itrakt.BaseURL = srv.URL
			t.Cleanup(func() { itrakt.BaseURL = orig })

			db, err := store.OpenSQLite(":memory:")
			if err != nil {
				t.Fatalf("OpenSQLite: %v", err)
			}
			t.Cleanup(func() { db.Close() })

			p, err := newPlugin(map[string]any{
				"client_id":    "test",
				"access_token": "tok",
				"type":         "movies",
				"list":         list,
			}, db)
			if err != nil {
				t.Fatalf("newPlugin(%s): %v", list, err)
			}

			entries, err := p.(*traktSourcePlugin).Generate(context.Background(), nil)
			if err != nil {
				t.Fatalf("Generate(%s): %v", list, err)
			}
			if len(entries) != 1 || entries[0].Title != "Dune" {
				t.Errorf("Generate(%s): got %v", list, entries)
			}
		})
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

	entries, err := p.(*traktSourcePlugin).Generate(context.Background(), nil)
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
		"client_id": "test",
		"type":      "movies",
		"list":      "trending",
	}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}

	entries, err := p.(*traktSourcePlugin).Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Title != "Inception" {
		t.Errorf("first entry title: got %q", entries[0].Title)
	}
	if v := entries[0].GetString(entry.FieldTitle); v != "Inception" {
		t.Errorf("FieldTitle: got %q", v)
	}
	// type=movies → media_type=movie on every emitted entry.
	for _, e := range entries {
		if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeMovie {
			t.Errorf("media_type: got %q, want %q", v, entry.MediaTypeMovie)
		}
	}
	const wantURL = "https://trakt.tv/movies/inception-2010"
	if entries[0].URL != wantURL {
		t.Errorf("URL: got %q, want %q", entries[0].URL, wantURL)
	}
	if v, _ := entries[0].Get("trakt_imdb_id"); v != "tt1375666" {
		t.Errorf("trakt_imdb_id: got %v", v)
	}
}

func TestEnrichesFromListResponse(t *testing.T) {
	// Trakt list responses use extended=full and carry rating, votes, genres,
	// overview, plus runtime/language/country/trailer/homepage/certification
	// (and tagline for movies / network+status+first_aired for shows).
	// Confirm the source surfaces all of them via Set*Info so downstream
	// filters can act on them without a redundant metainfo_trakt round-trip.
	srv := makeServer(t, []map[string]any{
		{
			"title":         "Inception",
			"year":          2010,
			"overview":      "Dream within a dream.",
			"rating":        8.5,
			"votes":         12000,
			"genres":        []any{"action", "sci-fi"},
			"runtime":       148,
			"country":       "us",
			"language":      "en",
			"trailer":       "https://youtube.com/watch?v=inception",
			"homepage":      "https://inception.example",
			"certification": "PG-13",
			"tagline":       "Your mind is the scene of the crime.",
			"released":      "2010-07-16",
			"ids": map[string]any{
				"trakt": 1, "slug": "inception-2010",
				"imdb": "tt1375666", "tmdb": 27205,
			},
		},
	})
	orig := itrakt.BaseURL
	itrakt.BaseURL = srv.URL
	t.Cleanup(func() { itrakt.BaseURL = orig })

	p, err := newPlugin(map[string]any{
		"client_id": "test", "type": "movies", "list": "trending",
	}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	entries, err := p.(*traktSourcePlugin).Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]

	checks := []struct {
		field string
		want  any
	}{
		{entry.FieldEnriched, true},
		{entry.FieldTitle, "Inception"},
		{entry.FieldDescription, "Dream within a dream."},
		{entry.FieldVideoYear, 2010},
		{entry.FieldVideoRating, 8.5},
		{entry.FieldVideoVotes, 12000},
		{entry.FieldVideoRuntime, 148},
		{entry.FieldVideoLanguage, "English"},
		{entry.FieldVideoCountry, "United States"},
		{entry.FieldVideoContentRating, "PG-13"},
		{entry.FieldVideoHomepage, "https://inception.example"},
		{entry.FieldVideoImdbID, "tt1375666"},
		{entry.FieldMovieTagline, "Your mind is the scene of the crime."},
	}
	for _, c := range checks {
		got, _ := e.Get(c.field)
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.field, got, c.want)
		}
	}
	trailers, _ := e.Get(entry.FieldVideoTrailers)
	urls, _ := trailers.([]string)
	if len(urls) != 1 || urls[0] != "https://youtube.com/watch?v=inception" {
		t.Errorf("%s: got %v", entry.FieldVideoTrailers, trailers)
	}
}

func TestEnrichesShowFromListResponse(t *testing.T) {
	// Shows surface the series-only fields (network, status, first_aired) on
	// top of the shared VideoInfo.
	srv := makeServerForType(t, "show", []map[string]any{
		{
			"title":       "Breaking Bad",
			"year":        2008,
			"overview":    "A chemistry teacher.",
			"rating":      9.2,
			"votes":       50000,
			"genres":      []any{"drama", "crime"},
			"runtime":     47,
			"country":     "us",
			"language":    "en",
			"network":     "AMC",
			"status":      "ended",
			"first_aired": "2008-01-20T05:00:00.000Z",
			"ids": map[string]any{
				"trakt": 1, "slug": "breaking-bad",
				"imdb": "tt0903747", "tvdb": 81189,
			},
		},
	})
	orig := itrakt.BaseURL
	itrakt.BaseURL = srv.URL
	t.Cleanup(func() { itrakt.BaseURL = orig })

	p, err := newPlugin(map[string]any{
		"client_id": "test", "type": "shows", "list": "trending",
	}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	entries, err := p.(*traktSourcePlugin).Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if v := e.GetString(entry.FieldSeriesNetwork); v != "AMC" {
		t.Errorf("series_network: got %q, want AMC", v)
	}
	if v := e.GetString(entry.FieldSeriesStatus); v != "ended" {
		t.Errorf("series_status: got %q, want ended", v)
	}
	got, _ := e.Get(entry.FieldSeriesFirstAirDate)
	tval, ok := got.(time.Time)
	if !ok {
		t.Fatalf("series_first_air_date: not a time.Time, got %T (%v)", got, got)
	}
	if tval.Format("2006-01-02") != "2008-01-20" {
		t.Errorf("series_first_air_date: got %v", tval)
	}
}
