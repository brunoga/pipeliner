package tvdb

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func makeServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": []map[string]any{
					{
						"tvdb_id":          "81189",
						"name":             "Breaking Bad",
						"year":             "2008",
						"slug":             "breaking-bad",
						"originalLanguage": "eng",
						"image_url":        "https://artworks.thetvdb.com/banners/posters/81189-1.jpg",
						"genres":           []string{"Drama", "Crime"},
					},
				},
				"status": "success",
			})
		case "/v4/series/81189/episodes/official":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]any{
					"episodes": []map[string]any{
						{"id": 111, "seasonNumber": 1, "number": 1, "name": "Pilot", "aired": "2008-01-20"},
					},
				},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// makeServerSparseSearch returns a server whose search result omits genres and
// language, simulating the inconsistency seen in the real TVDB API. The
// extended endpoint provides the missing data.
func makeServerSparseSearch() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": []map[string]any{
					{"tvdb_id": "81189", "name": "Breaking Bad", "year": "2008", "slug": "breaking-bad"},
				},
				"status": "success",
			})
		case "/v4/series/81189/extended":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]any{
					"originalLanguage": "eng",
					"genres": []map[string]any{
						{"id": 3, "name": "Drama"},
						{"id": 4, "name": "Crime"},
					},
				},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func makePlugin(t *testing.T, srv *httptest.Server) *tvdbPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	raw, err := newPlugin(map[string]any{"api_key": "test-key"}, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p := raw.(*tvdbPlugin)
	p.client.BaseURL = srv.URL + "/v4"
	return p
}

func TestAnnotateSeries(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	p := makePlugin(t, srv)

	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("tvdb_series_name"); v != "Breaking Bad" {
		t.Errorf("tvdb_series_name: got %q", v)
	}
	if v := e.GetString("tvdb_id"); v != "81189" {
		t.Errorf("tvdb_id: got %q", v)
	}
	if v := e.GetString("tvdb_episode_name"); v != "Pilot" {
		t.Errorf("tvdb_episode_name: got %q", v)
	}
	if v := e.GetString("tvdb_air_date"); v != "2008-01-20" {
		t.Errorf("tvdb_air_date: got %q", v)
	}
	if v := e.GetString("tvdb_language"); v != "English" {
		t.Errorf("tvdb_language: got %q", v)
	}
	if v := e.GetString("tvdb_poster"); v != "https://artworks.thetvdb.com/banners/posters/81189-1.jpg" {
		t.Errorf("tvdb_poster: got %q", v)
	}
}

func TestAnnotateExtendedFallback(t *testing.T) {
	srv := makeServerSparseSearch()
	defer srv.Close()

	p := makePlugin(t, srv)

	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("tvdb_language"); v != "English" {
		t.Errorf("tvdb_language: got %q, want English", v)
	}
	genres, _ := e.Get("tvdb_genres")
	names, _ := genres.([]string)
	if len(names) != 2 || names[0] != "Drama" || names[1] != "Crime" {
		t.Errorf("tvdb_genres: got %v", genres)
	}
}

func TestAnnotateNonSeries(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	p := makePlugin(t, srv)

	e := entry.New("Some Random Movie 2023", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("tvdb_series_name"); v != "" {
		t.Errorf("non-series should not set tvdb_series_name, got %q", v)
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("metainfo_tvdb")
	if !ok {
		t.Fatal("metainfo_tvdb not registered")
	}
	if d.PluginPhase != plugin.PhaseMetainfo {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}
