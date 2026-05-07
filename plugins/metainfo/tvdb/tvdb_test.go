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
		case "/v4/series/81189/extended":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]any{
					"name":             "Breaking Bad",
					"slug":             "breaking-bad",
					"year":             "2008",
					"image":            "https://artworks.thetvdb.com/banners/posters/81189-1.jpg",
					"originalLanguage": "eng",
					"originalCountry":  "usa",
					"originalNetwork":  map[string]any{"name": "AMC"},
					"firstAired":       "2008-01-20",
					"lastAired":        "2013-09-29",
					"score":            99869.0,
					"status":           map[string]any{"name": "Ended"},
					"genres":           []map[string]any{{"name": "Drama"}, {"name": "Crime"}},
					"trailers":         []map[string]any{{"url": "https://youtube.com/watch?v=xyz", "language": "eng"}},
					"contentRatings":   []map[string]any{{"name": "TV-MA", "video_country": "usa"}},
					"characters":       []map[string]any{{"personName": "Bryan Cranston", "type": 3, "sort": 1}},
				},
				"status": "success",
			})
		case "/v4/series/81189/episodes/official":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]any{
					"episodes": []map[string]any{
						{"id": 111, "seasonNumber": 1, "number": 1, "name": "Pilot", "aired": "2008-01-20", "runtime": 47, "image": "https://artworks.thetvdb.com/banners/episodes/81189/1.jpg"},
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
					"name":             "Breaking Bad",
					"originalLanguage": "eng",
					"originalCountry":  "usa",
					"firstAired":       "2008-01-20",
					"lastAired":        "2013-09-29",
					"score":            99869.0,
					"status":           map[string]any{"name": "Ended"},
					"genres": []map[string]any{
						{"id": 3, "name": "Drama"},
						{"id": 4, "name": "Crime"},
					},
					"trailers": []map[string]any{
						{"url": "https://youtube.com/watch?v=abc123", "language": "eng"},
					},
					"contentRatings": []map[string]any{
						{"name": "TV-MA", "video_country": "usa"},
					},
					"aliases": []map[string]any{
						{"language": "spa", "name": "Breaking Bad (Spanish)"},
					},
					"characters": []map[string]any{
						{"personName": "Bryan Cranston", "type": 3, "sort": 1},
						{"personName": "Aaron Paul", "type": 3, "sort": 2},
					},
					"nameTranslations": []map[string]any{
						{"language": "eng", "name": "Breaking Bad"},
					},
				},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// makeServerNoResults returns a server that always returns empty search results.
func makeServerNoResults() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "status": "success"}) //nolint:errcheck
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

	// Provider-specific fields (only ID and slug remain).
	if v := e.GetString("tvdb_id"); v != "81189" {
		t.Errorf("tvdb_id: got %q", v)
	}
	if v := e.GetString("tvdb_slug"); v != "breaking-bad" {
		t.Errorf("tvdb_slug: got %q", v)
	}
	// Standard fields.
	if v := e.GetString("title"); v != "Breaking Bad" {
		t.Errorf("title: got %q, want Breaking Bad", v)
	}
	if v := e.GetString("video_language"); v != "English" {
		t.Errorf("language: got %q, want English", v)
	}
	if v := e.GetString("video_country"); v != "United States" {
		t.Errorf("country: got %q, want United States", v)
	}
	if v := e.GetString("series_network"); v != "AMC" {
		t.Errorf("network: got %q, want AMC", v)
	}
	if v := e.GetString("video_poster"); v == "" {
		t.Error("poster should be set")
	}
	if v := e.GetString("series_status"); v != "Ended" {
		t.Errorf("status: got %q, want Ended", v)
	}
	if v := e.GetString("video_content_rating"); v != "TV-MA" {
		t.Errorf("content_rating: got %q, want TV-MA", v)
	}
	trailers, _ := e.Get("video_trailers")
	if urls, _ := trailers.([]string); len(urls) == 0 {
		t.Error("trailers should be set")
	}
	// Episode standard fields.
	if v := e.GetString("series_episode_title"); v != "Pilot" {
		t.Errorf("episode_title: got %q, want Pilot", v)
	}
	if v := e.GetString("series_episode_air_date"); v != "2008-01-20" {
		t.Errorf("episode_air_date: got %q", v)
	}
	if v := e.GetInt("series_season"); v != 1 {
		t.Errorf("season: got %d, want 1", v)
	}
	if v := e.GetInt("series_episode"); v != 1 {
		t.Errorf("episode: got %d, want 1", v)
	}
	if v := e.GetString("series_episode_id"); v != "S01E01" {
		t.Errorf("episode_id: got %q, want S01E01", v)
	}
	if v := e.GetInt("video_runtime"); v != 47 {
		t.Errorf("runtime: got %d, want 47", v)
	}
	if v := e.GetString("series_episode_image"); v == "" {
		t.Error("episode_image should be set")
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
	// Standard fields populated from extended data.
	if v := e.GetString("video_language"); v != "English" {
		t.Errorf("language: got %q, want English", v)
	}
	if v := e.GetString("video_country"); v != "United States" {
		t.Errorf("country: got %q, want United States", v)
	}
	genres, _ := e.Get("video_genres")
	names, _ := genres.([]string)
	if len(names) != 2 || names[0] != "Drama" || names[1] != "Crime" {
		t.Errorf("genres: got %v", genres)
	}
	if v := e.GetString("series_status"); v != "Ended" {
		t.Errorf("status: got %q, want Ended", v)
	}
	trailers, _ := e.Get("video_trailers")
	trailerURLs, _ := trailers.([]string)
	if len(trailerURLs) != 1 || trailerURLs[0] != "https://youtube.com/watch?v=abc123" {
		t.Errorf("trailers: got %v", trailers)
	}
	if v := e.GetString("video_content_rating"); v != "TV-MA" {
		t.Errorf("content_rating: got %q, want TV-MA", v)
	}
	aliases, _ := e.Get("video_aliases")
	aliasNames, _ := aliases.([]string)
	if len(aliasNames) != 1 {
		t.Errorf("aliases: got %v", aliases)
	}
	if score, _ := e.Get("video_rating"); score == nil {
		t.Error("rating should be set")
	}
	cast, _ := e.Get("video_cast")
	castNames, _ := cast.([]string)
	if len(castNames) != 2 || castNames[0] != "Bryan Cranston" {
		t.Errorf("cast: got %v", cast)
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
	if v := e.GetString("title"); v != "" {
		t.Errorf("non-series should not set title, got %q", v)
	}
}

func TestEnrichedSetOnSuccess(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	p := makePlugin(t, srv)
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.GetBool("enriched") {
		t.Error("enriched should be true when TVDB finds the show")
	}
}

func TestEnrichedNotSetOnNoResults(t *testing.T) {
	srv := makeServerNoResults()
	defer srv.Close()

	p := makePlugin(t, srv)
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.GetBool("enriched") {
		t.Error("enriched should not be set when TVDB returns no results")
	}
}

// makeServerYearStrip serves empty results for the full name (with year) and
// real results for the stripped name, simulating a series whose release title
// includes a production year that TVDB doesn't include in the show name.
func makeServerYearStrip() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			q := r.URL.Query().Get("query")
			if q == "Dark 2017" {
				// Full name with year — return empty.
				json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "status": "success"}) //nolint:errcheck
			} else {
				// Stripped name — return a result.
				json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
					"data": []map[string]any{
						{"tvdb_id": "322190", "name": "Dark", "year": "2017", "slug": "dark"},
					},
					"status": "success",
				})
			}
		case "/v4/series/322190/extended":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]any{"originalLanguage": "deu", "originalCountry": "deu"},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestAnnotateTrailingYearParallelSearch(t *testing.T) {
	srv := makeServerYearStrip()
	defer srv.Close()

	p := makePlugin(t, srv)

	// Title contains "2017" as a production year right before the episode ID.
	e := entry.New("Dark.2017.S01E01.1080p.WEBRip", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("title"); v != "Dark" {
		t.Errorf("title: got %q, want %q", v, "Dark")
	}
	if v := e.GetString("tvdb_id"); v != "322190" {
		t.Errorf("tvdb_id: got %q, want %q", v, "322190")
	}
}

func TestAnnotateParenthesizedYearParallelSearch(t *testing.T) {
	srv := makeServerYearStrip()
	defer srv.Close()

	p := makePlugin(t, srv)

	// Year is inside parentheses with no space: Show(2019)s01e12
	e := entry.New("Dark(2017)S01E01.1080p.WEBRip", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("title"); v != "Dark" {
		t.Errorf("title: got %q, want %q", v, "Dark")
	}
}

// makeServerForeignShow serves a show whose TVDB display name is the
// international title and whose original-language title differs — simulating
// e.g. "Money Heist" (display) vs "La Casa de Papel" (Spanish original).
func makeServerForeignShow() *httptest.Server {
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
						"tvdb_id":          "355774",
						"name":             "Money Heist",
						"year":             "2017",
						"slug":             "money-heist",
						"originalLanguage": "spa",
					},
				},
				"status": "success",
			})
		case "/v4/series/355774/extended":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]any{
					"originalLanguage": "spa",
					"originalCountry":  "esp",
					"status":           map[string]any{"name": "Ended"},
					"nameTranslations": []map[string]any{
						{"language": "spa", "name": "La Casa de Papel"},
						{"language": "eng", "name": "Money Heist"},
					},
				},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestOriginalTitleForeignShow(t *testing.T) {
	srv := makeServerForeignShow()
	defer srv.Close()

	p := makePlugin(t, srv)
	e := entry.New("Money.Heist.S01E01.1080p.WEBRip", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("video_original_title"); v != "La Casa de Papel" {
		t.Errorf("original_title: got %q, want %q", v, "La Casa de Papel")
	}
}

func TestOriginalTitleNotSetForEnglishShow(t *testing.T) {
	// Breaking Bad is English — original_title should not be set since
	// the original name matches the display name.
	srv := makeServerSparseSearch()
	defer srv.Close()

	p := makePlugin(t, srv)
	e := entry.New("Breaking.Bad.S01E01.1080p.WEBRip", "http://x.com/a")
	if err := p.Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("video_original_title"); v != "" {
		t.Errorf("original_title should not be set for English shows, got %q", v)
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
