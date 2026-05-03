package tvdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

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
					{"tvdb_id": 81189, "name": "Breaking Bad", "year": "2008", "slug": "breaking-bad"},
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

func TestAnnotateSeries(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	c := itvdb.New("test-key")
	c.BaseURL = srv.URL + "/v4"

	p := &tvdbPlugin{client: c}

	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("tvdb_series_name"); v != "Breaking Bad" {
		t.Errorf("tvdb_series_name: got %q", v)
	}
	if v := e.GetInt("tvdb_id"); v != 81189 {
		t.Errorf("tvdb_id: got %d", v)
	}
	if v := e.GetString("tvdb_episode_name"); v != "Pilot" {
		t.Errorf("tvdb_episode_name: got %q", v)
	}
	if v := e.GetString("tvdb_air_date"); v != "2008-01-20" {
		t.Errorf("tvdb_air_date: got %q", v)
	}
}

func TestAnnotateNonSeries(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	c := itvdb.New("test-key")
	c.BaseURL = srv.URL + "/v4"

	p := &tvdbPlugin{client: c}

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
