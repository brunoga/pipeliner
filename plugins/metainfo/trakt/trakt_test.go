package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

func tc() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func searchResponse(title string, year, traktID, tvdbID int, rating float64, genres []string) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
		IMDB  string `json:"imdb"`
		TMDB  int    `json:"tmdb"`
		TVDB  int    `json:"tvdb"`
	}
	type show struct {
		Title    string   `json:"title"`
		Year     int      `json:"year"`
		IDs      ids      `json:"ids"`
		Overview string   `json:"overview"`
		Rating   float64  `json:"rating"`
		Votes    int      `json:"votes"`
		Genres   []string `json:"genres"`
	}
	type item struct {
		Type  string  `json:"type"`
		Score float64 `json:"score"`
		Show  show    `json:"show"`
	}
	result := []item{{
		Type:  "show",
		Score: 1000,
		Show: show{
			Title:    title,
			Year:     year,
			IDs:      ids{Trakt: traktID, Slug: "breaking-bad", IMDB: "tt0903747", TMDB: 1396, TVDB: tvdbID},
			Overview: "A chemistry teacher turns to crime.",
			Rating:   rating,
			Votes:    500000,
			Genres:   genres,
		},
	}}
	b, _ := json.Marshal(result)
	return b
}

func mockServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
}

func makePlugin(t *testing.T, cfg map[string]any) *traktMetaPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*traktMetaPlugin)
}

func TestAnnotateShow(t *testing.T) {
	body := searchResponse("Breaking Bad", 2008, 1, 81189, 9.4, []string{"drama", "crime"})
	srv := mockServer(t, body)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1")

	if err := p.Annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetInt("trakt_id"); v != 1 {
		t.Errorf("trakt_id: got %d, want 1", v)
	}
	if v := e.GetString("trakt_title"); v != "Breaking Bad" {
		t.Errorf("trakt_title: got %q, want %q", v, "Breaking Bad")
	}
	if v := e.GetInt("trakt_year"); v != 2008 {
		t.Errorf("trakt_year: got %d, want 2008", v)
	}
	if v := e.GetString("trakt_imdb_id"); v != "tt0903747" {
		t.Errorf("trakt_imdb_id: got %q", v)
	}
	if v := e.GetInt("trakt_tvdb_id"); v != 81189 {
		t.Errorf("trakt_tvdb_id: got %d, want 81189", v)
	}
	if v := e.GetString("trakt_genres"); v == "" {
		t.Error("trakt_genres should be set")
	}
	if v := e.GetString("trakt_overview"); v == "" {
		t.Error("trakt_overview should be set")
	}
}

func TestAnnotateNonParseableTitle(t *testing.T) {
	srv := mockServer(t, []byte("[]"))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Just A Random Article", "http://example.com/1")

	if err := p.Annotate(context.Background(), tc(), e); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if e.GetString("trakt_title") != "" {
		t.Error("non-parseable title should not set any fields")
	}
}

func TestAnnotateNoResults(t *testing.T) {
	srv := mockServer(t, []byte("[]"))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Unknown.Show.S01E01.720p", "http://example.com/1")

	if err := p.Annotate(context.Background(), tc(), e); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if e.GetInt("trakt_id") != 0 {
		t.Error("no-result search should not set trakt_id")
	}
}

func TestAnnotateMovie(t *testing.T) {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
		IMDB  string `json:"imdb"`
		TMDB  int    `json:"tmdb"`
	}
	type movie struct {
		Title    string   `json:"title"`
		Year     int      `json:"year"`
		IDs      ids      `json:"ids"`
		Overview string   `json:"overview"`
		Rating   float64  `json:"rating"`
		Votes    int      `json:"votes"`
		Genres   []string `json:"genres"`
	}
	type item struct {
		Type  string  `json:"type"`
		Movie movie   `json:"movie"`
		Score float64 `json:"score"`
	}
	body, _ := json.Marshal([]item{{
		Type:  "movie",
		Score: 1000,
		Movie: movie{Title: "Inception", Year: 2010, IDs: ids{Trakt: 42, IMDB: "tt1375666", TMDB: 27205}},
	}})

	srv := mockServer(t, body)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "movies"})
	e := entry.New("Inception.2010.1080p.BluRay", "http://example.com/1")

	if err := p.Annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetInt("trakt_id"); v != 42 {
		t.Errorf("trakt_id: got %d, want 42", v)
	}
	if v := e.GetString("trakt_imdb_id"); v != "tt1375666" {
		t.Errorf("trakt_imdb_id: got %q", v)
	}
	// Movies don't have tvdb_id.
	if v := e.GetInt("trakt_tvdb_id"); v != 0 {
		t.Error("movies should not set trakt_tvdb_id")
	}
}

func TestMissingClientID(t *testing.T) {
	_, err := newPlugin(map[string]any{"type": "shows"}, nil)
	if err == nil {
		t.Error("expected error for missing client_id")
	}
}

func TestInvalidType(t *testing.T) {
	_, err := newPlugin(map[string]any{"client_id": "key", "type": "podcasts"}, nil)
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestPluginRegistered(t *testing.T) {
	if _, ok := plugin.Lookup("metainfo_trakt"); !ok {
		t.Error("metainfo_trakt plugin not registered")
	}
}
