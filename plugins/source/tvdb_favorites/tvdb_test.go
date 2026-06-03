package tvdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// makeTVDBServer builds a minimal TVDB-shaped test server.
func makeTVDBServer(t *testing.T, favoriteIDs []int, series []itvdb.Series) *httptest.Server {
	t.Helper()
	byID := make(map[string]itvdb.Series, len(series))
	for _, s := range series {
		byID[s.ID] = s
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encode := func(v any) { json.NewEncoder(w).Encode(v) }
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/login":
			encode(map[string]any{"data": map[string]any{"token": "tok"}, "status": "success"})
		case r.Method == http.MethodGet && r.URL.Path == "/user/favorites":
			encode(map[string]any{"data": map[string]any{"series": favoriteIDs}, "status": "success"})
		default:
			var id int
			if _, err := fmt.Sscanf(r.URL.Path, "/series/%d", &id); err == nil {
				if s, ok := byID[fmt.Sprintf("%d", id)]; ok {
					encode(map[string]any{"data": s, "status": "success"})
					return
				}
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMissingAPIKey(t *testing.T) {
	_, err := newPlugin(map[string]any{"user_pin": "pin"}, nil)
	if err == nil {
		t.Fatal("expected error for missing api_key")
	}
}

func TestMissingUserPin(t *testing.T) {
	_, err := newPlugin(map[string]any{"api_key": "key"}, nil)
	if err == nil {
		t.Fatal("expected error for missing user_pin")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("tvdb_favorites")
	if !ok {
		t.Fatal("tvdb_favorites not registered")
	}
	if d.Role != plugin.RoleSource {
		t.Errorf("phase: got %v", d.Role)
	}
}

func TestRunReturnsEntries(t *testing.T) {
	srv := makeTVDBServer(t, []int{1, 2}, []itvdb.Series{
		{ID: "1", Name: "Breaking Bad", Slug: "breaking-bad", Year: "2008"},
		{ID: "2", Name: "Better Call Saul", Slug: "better-call-saul"},
	})

	p, err := newPlugin(map[string]any{"api_key": "key", "user_pin": "pin"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p.(*tvdbSourcePlugin).client.BaseURL = srv.URL

	entries, err := p.(*tvdbSourcePlugin).Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Title != "Breaking Bad" {
		t.Errorf("first title: got %q", entries[0].Title)
	}
	if v := entries[0].GetString(entry.FieldTitle); v != "Breaking Bad" {
		t.Errorf("FieldTitle: got %q", v)
	}
	// TVDB only catalogs TV — every emitted entry must be tagged as series.
	for _, e := range entries {
		if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeSeries {
			t.Errorf("media_type: got %q, want %q", v, entry.MediaTypeSeries)
		}
	}
	if entries[0].URL != "https://thetvdb.com/series/breaking-bad" {
		t.Errorf("URL: got %q", entries[0].URL)
	}
	if v, _ := entries[0].Get("tvdb_year"); v != "2008" {
		t.Errorf("tvdb_year: got %v", v)
	}
	if v, _ := entries[1].Get("tvdb_year"); v != nil {
		t.Errorf("tvdb_year should be absent when empty, got %v", v)
	}
}

func TestEnrichesFromSeriesResponse(t *testing.T) {
	// Verify TVDB favorites surface the same standard VideoInfo/SeriesInfo
	// fields that metainfo_tvdb would otherwise populate from a fresh search.
	// Score is a popularity ranking per the TVDB API, so it goes to
	// video_popularity (not video_rating).
	srv := makeTVDBServer(t, []int{1}, []itvdb.Series{
		{
			ID: "1", Name: "Severance", Slug: "severance",
			Overview:   "An employee discovers a disturbing truth.",
			Genres:     []string{"Drama", "Sci-Fi"},
			Network:    "Apple TV+",
			Language:   "eng",
			Country:    "usa",
			ImageURL:   "https://example.com/poster.jpg",
			FirstAired: "2022-02-18",
			Score:      12345.6,
		},
	})

	p, err := newPlugin(map[string]any{"api_key": "key", "user_pin": "pin"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p.(*tvdbSourcePlugin).client.BaseURL = srv.URL

	entries, err := p.(*tvdbSourcePlugin).Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatalf("Run: %v", err)
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
		{entry.FieldTitle, "Severance"},
		{entry.FieldDescription, "An employee discovers a disturbing truth."},
		{entry.FieldVideoLanguage, "English"},
		{entry.FieldVideoCountry, "United States"},
		{entry.FieldVideoPoster, "https://example.com/poster.jpg"},
		{entry.FieldSeriesNetwork, "Apple TV+"},
		{entry.FieldVideoYear, 2022},
		{entry.FieldVideoPopularity, 12345.6},
	}
	for _, c := range checks {
		got, _ := e.Get(c.field)
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.field, got, c.want)
		}
	}
	if v, _ := e.Get(entry.FieldVideoRating); v != nil {
		t.Errorf("%s: should not be set by tvdb_favorites (got %v)", entry.FieldVideoRating, v)
	}
	if v, _ := e.Get(entry.FieldVideoGenres); fmt.Sprint(v) != "[Drama Sci-Fi]" {
		t.Errorf("%s: got %v", entry.FieldVideoGenres, v)
	}
	if v, _ := e.Get("tvdb_slug"); v != "severance" {
		t.Errorf("tvdb_slug: got %v", v)
	}
}

func TestRunSkipsMissingShows(t *testing.T) {
	// Favorites list includes ID 99 which the server doesn't know about.
	srv := makeTVDBServer(t, []int{1, 99}, []itvdb.Series{
		{ID: "1", Name: "Firefly", Slug: "firefly"},
	})

	p, err := newPlugin(map[string]any{"api_key": "key", "user_pin": "pin"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p.(*tvdbSourcePlugin).client.BaseURL = srv.URL

	entries, err := p.(*tvdbSourcePlugin).Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("want 1 entry (missing ID skipped), got %d", len(entries))
	}
}
