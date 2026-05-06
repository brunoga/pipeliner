package tmdb

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
	itmdb "github.com/brunoga/pipeliner/internal/tmdb"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func makeServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"results": []map[string]any{
					{
						"id": 27205, "title": "Inception",
						"release_date": "2010-07-16", "popularity": 99.5,
						"vote_average": 8.8, "overview": "A thief...",
					},
				},
				"page": 1, "total_results": 1,
			})
		case "/3/movie/27205":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"id": 27205, "title": "Inception", "release_date": "2010-07-16",
				"runtime": 148, "tagline": "Your mind is the scene.",
				"imdb_id": "tt1375666",
				"genres":  []map[string]any{{"id": 28, "name": "Action"}, {"id": 878, "name": "Science Fiction"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestAnnotateMovie(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"

	p := &tmdbPlugin{client: c}

	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("tmdb_title"); v != "Inception" {
		t.Errorf("tmdb_title: got %q", v)
	}
	if v := e.GetInt("tmdb_id"); v != 27205 {
		t.Errorf("tmdb_id: got %d", v)
	}
	if v := e.GetInt("tmdb_runtime"); v != 148 {
		t.Errorf("tmdb_runtime: got %d", v)
	}
	if v := e.GetString("tmdb_imdb_id"); v != "tt1375666" {
		t.Errorf("tmdb_imdb_id: got %q", v)
	}
	if v := e.GetString("tmdb_genres"); v != "Action, Science Fiction" {
		t.Errorf("tmdb_genres: got %q", v)
	}
}

func TestAnnotateNonMovie(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"

	p := &tmdbPlugin{client: c}

	e := entry.New("Show.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("tmdb_title"); v != "" {
		t.Errorf("series title should not set tmdb_title, got %q", v)
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("metainfo_tmdb")
	if !ok {
		t.Fatal("metainfo_tmdb not registered")
	}
	if d.PluginPhase != plugin.PhaseMetainfo {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}
