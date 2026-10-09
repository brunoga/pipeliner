package tvdb

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
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
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{
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
			json.NewEncoder(w).Encode(map[string]any{
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
			json.NewEncoder(w).Encode(map[string]any{
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
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"tvdb_id": "81189", "name": "Breaking Bad", "year": "2008", "slug": "breaking-bad"},
				},
				"status": "success",
			})
		case "/v4/series/81189/extended":
			json.NewEncoder(w).Encode(map[string]any{
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
					"nameTranslations": []string{"eng", "spa"},
					"translations": map[string]any{
						"nameTranslations": []map[string]any{
							{"language": "eng", "name": "Breaking Bad"},
						},
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
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "status": "success"})
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
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
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q, want %q", v, entry.MediaTypeSeries)
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
	if v := e.GetTime("series_episode_air_date"); v.Format("2006-01-02") != "2008-01-20" {
		t.Errorf("episode_air_date: got %v", v)
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
	if v := e.GetInt(entry.FieldVideoYear); v != 2008 {
		t.Errorf("video_year: got %d, want 2008 (derived from firstAired)", v)
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
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
	// TVDB Score is a popularity ranking, not a 0-10 user rating — see
	// internal/tvdb/client.go's Score field comment. The plugin routes it to
	// video_popularity; video_rating stays empty for TVDB-only enrichment.
	if pop, _ := e.Get("video_popularity"); pop == nil {
		t.Error("popularity should be set")
	}
	if v, _ := e.Get("video_rating"); v != nil {
		t.Errorf("video_rating: should not be set by TVDB (got %v)", v)
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	// Check Fields directly: GetString("title") would fall back to the
	// raw struct e.Title; we want to verify Fields was not populated.
	if _, ok := e.Fields["title"]; ok {
		t.Errorf("non-series should not set Fields[\"title\"], got %q", e.Fields["title"])
	}
}

func TestEnrichedSetOnSuccess(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	p := makePlugin(t, srv)
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
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
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			q := r.URL.Query().Get("query")
			if q == "Dark 2017" {
				// Full name with year — return empty.
				json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "status": "success"})
			} else {
				// Stripped name — return a result.
				json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{
						{"tvdb_id": "322190", "name": "Dark", "year": "2017", "slug": "dark"},
					},
					"status": "success",
				})
			}
		case "/v4/series/322190/extended":
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]any{"originalLanguage": "deu", "originalCountry": "deu"},
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
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
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{
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
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"originalLanguage": "spa",
					"originalCountry":  "esp",
					"status":           map[string]any{"name": "Ended"},
					"nameTranslations": []string{"spa", "eng"},
					"translations": map[string]any{
						"nameTranslations": []map[string]any{
							{"language": "spa", "name": "La Casa de Papel"},
							{"language": "eng", "name": "Money Heist"},
						},
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("video_original_title"); v != "" {
		t.Errorf("original_title should not be set for English shows, got %q", v)
	}
}

func TestEmptySearchNotCached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case "/v4/search":
			callCount++
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "status": "success"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := makePlugin(t, srv)
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	p.annotate(context.Background(), makeCtx(), e)
	p.annotate(context.Background(), makeCtx(), e)

	if callCount < 2 {
		t.Errorf("empty search result should not be cached; API called %d times, want ≥2", callCount)
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("metainfo_tvdb")
	if !ok {
		t.Fatal("metainfo_tvdb not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("phase: got %v", d.Role)
	}
}

// TestAnnotateExtendedStaleFallback proves that a transient extended-fetch
// failure does not discard genres already fetched: the plugin serves the
// stale cached extended record instead of falling through to the genre-less
// search data. This is the regression for a show losing its genres (and being
// dropped by the genre-based auto-favorite filter) on a TVDB blip.
func TestAnnotateExtendedStaleFallback(t *testing.T) {
	failExtended := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"token": "jwt"}, "status": "success"}) //nolint:errcheck
		case r.URL.Path == "/v4/search":
			// Search omits genres, as the real endpoint does.
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{ //nolint:errcheck
				"tvdb_id": "81189", "name": "Breaking Bad", "year": "2008", "slug": "breaking-bad", "originalLanguage": "eng", "genres": nil,
			}}, "status": "success"})
		case strings.HasSuffix(r.URL.Path, "/extended"):
			if failExtended {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{ //nolint:errcheck
				"name": "Breaking Bad", "slug": "breaking-bad", "year": "2008",
				"originalLanguage": "eng", "originalCountry": "usa",
				"status": map[string]any{"name": "Ended"},
				"genres": []map[string]any{{"name": "Drama"}, {"name": "Crime"}},
			}, "status": "success"})
		case strings.HasSuffix(r.URL.Path, "/episodes/official"):
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"episodes": []map[string]any{ //nolint:errcheck
				{"seasonNumber": 1, "number": 1, "name": "Pilot", "aired": "2008-01-20"},
			}}, "status": "success"})
		}
	}))
	defer srv.Close()

	// Short TTL so the extended cache expires between the two runs, forcing a
	// live re-fetch on the second (which we then make fail).
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	raw, err := newPlugin(map[string]any{"api_key": "k", "cache_ttl": "20ms"}, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p := raw.(*tvdbPlugin)
	p.client.BaseURL = srv.URL + "/v4"

	genresOf := func(e *entry.Entry) []string {
		v, _ := e.Get("video_genres")
		gs, _ := v.([]string)
		return gs
	}

	// First run: extended succeeds, genres cached.
	e1 := entry.New("Breaking.Bad.S01E01.720p", "http://x/a")
	if err := p.annotate(context.Background(), makeCtx(), e1); err != nil {
		t.Fatal(err)
	}
	if len(genresOf(e1)) == 0 {
		t.Fatal("first run should populate genres from the extended endpoint")
	}

	// Expire the cache and break the extended endpoint.
	time.Sleep(30 * time.Millisecond)
	failExtended = true

	// Second run: extended fetch fails, but the stale cache preserves genres.
	e2 := entry.New("Breaking.Bad.S01E01.720p", "http://x/b")
	if err := p.annotate(context.Background(), makeCtx(), e2); err != nil {
		t.Fatal(err)
	}
	if got := genresOf(e2); len(got) == 0 {
		t.Error("stale-cache fallback should preserve genres when the extended fetch fails")
	}
}

// TestEnrichedNotSetWhenExtendedUnavailable: extended fetch fails with nothing
// cached → the entry must NOT be marked enriched, so a downstream
// require(["enriched"]) holds it for the next run instead of letting a
// genre-gated branch silently drop it.
func TestEnrichedNotSetWhenExtendedUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"token": "jwt"}, "status": "success"}) //nolint:errcheck
		case r.URL.Path == "/v4/search":
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{ //nolint:errcheck
				"tvdb_id": "81189", "name": "Breaking Bad", "year": "2008", "slug": "breaking-bad",
			}}, "status": "success"})
		default: // extended + episodes both fail
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	p := makePlugin(t, srv)
	e := entry.New("Breaking.Bad.S01E01.720p", "http://x/a")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.GetBool("enriched") {
		t.Error("enriched must not be set when extended data is unavailable and nothing is cached")
	}
	// Provider id fields may still be present for debugging, but the gate field
	// is what matters.
}

func TestPickSeriesPrefersExactTitle(t *testing.T) {
	rs := []itvdb.Series{
		{ID: "1", Name: "Breaking Bad: The Movie"}, // companion outranks in relevance
		{ID: "2", Name: "Breaking Bad"},
	}
	if got, ok := pickSeries(rs, "Breaking Bad", 0); !ok || got.ID != "2" {
		t.Errorf("exact title should win, got id %s (%s) ok=%v", got.ID, got.Name, ok)
	}
	// No exact match, but "Breaking Bad: The Movie" contains "Breaking" as a
	// whole word, so it is a plausible match and still wins.
	if got, ok := pickSeries(rs, "Breaking", 0); !ok || got.ID != "1" {
		t.Errorf("a whole-word containment should settle it, got %s ok=%v", got.ID, ok)
	}
	// Normalization: punctuation differences still count as exact.
	rs = []itvdb.Series{
		{ID: "1", Name: "Marvels Agents"},
		{ID: "2", Name: "Marvel's Agents"},
	}
	if got, ok := pickSeries(rs, "Marvels Agents", 0); !ok || got.ID != "1" {
		t.Errorf("first exact match should win, got %s ok=%v", got.ID, ok)
	}
}

// TestPickSeriesBrothers2026 is the reported failure, with the result set
// TVDB actually returns. "Brothers 2026 S01E03 Little Woody ..." parses to the
// series name "Brothers 2026" — which no show is called — so the exact
// comparison never matched and results[0] won. Every episode of the 2026 show
// was enriched as Big Brother: wrong name, overview, network and poster in the
// notification, while the download itself was correct.
func TestPickSeriesBrothers2026(t *testing.T) {
	// Verbatim from GET /v4/search?query=Brothers%202026&type=series.
	full := []itvdb.Series{
		{ID: "440642", Name: "Big Brother (2023)", Year: "2023"},
		{ID: "442120", Name: "Celebrity Big Brother (2024)", Year: "2024"},
		{ID: "448114", Name: "Brothers", Year: "2026"},
		{ID: "471979", Name: "สองหัวใจ", Year: "2026"},
	}
	// What the old code did: no name equals "Brothers 2026", so it returned
	// results[0]. Pinning that here is what makes this a regression test — if
	// the fallback ever becomes unconditional again, this is the show it
	// would hand back.
	if full[0].Name != "Big Brother (2023)" {
		t.Fatal("fixture no longer reproduces the reported failure")
	}

	got, ok := pickSeries(full, "Brothers 2026", 2026)
	if !ok {
		t.Fatal("the right show is in the results; it must be found")
	}
	if got.ID != "448114" {
		t.Errorf("picked %q (%s), want Brothers 2026 (448114)", got.Name, got.ID)
	}
}

// The year disambiguates, which is the thing it is actually good for:
// "Brothers" alone is three different shows.
func TestPickSeriesUsesTheYearToDisambiguate(t *testing.T) {
	rs := []itvdb.Series{
		{ID: "71477", Name: "Brothers", Year: "1984"},
		{ID: "387262", Name: "BROTHERS", Year: "2014"},
		{ID: "448114", Name: "Brothers", Year: "2026"},
	}
	for year, want := range map[int]string{1984: "71477", 2014: "387262", 2026: "448114"} {
		if got, ok := pickSeries(rs, "Brothers "+itoa(year), year); !ok || got.ID != want {
			t.Errorf("year %d: picked %s, want %s", year, got.ID, want)
		}
	}
	// With no year stated, relevance order decides — there is nothing better.
	if got, ok := pickSeries(rs, "Brothers", 0); !ok || got.ID != "71477" {
		t.Errorf("no year: got %s, want the first result", got.ID)
	}
}

// The floor: a result sharing no whole word is never accepted, however TVDB
// ranked it. This is the guard that keeps "Brothers" off "Big Brother" even
// when the right show is missing from the results entirely.
func TestPickSeriesRefusesAnUnrelatedTopHit(t *testing.T) {
	onlyWrong := []itvdb.Series{
		{ID: "440642", Name: "Big Brother (2023)", Year: "2023"},
		{ID: "442120", Name: "Celebrity Big Brother (2024)", Year: "2024"},
	}
	if got, ok := pickSeries(onlyWrong, "Brothers 2026", 2026); ok {
		t.Errorf("should have refused, picked %q (%s)", got.Name, got.ID)
	}
	// "Brother" and "Brothers" are different words, and that is the whole
	// distinction here — no edit-distance tolerance, as match.Fuzzy documents.
	if _, ok := pickSeries([]itvdb.Series{{ID: "1", Name: "Big Brother"}}, "Brothers", 0); ok {
		t.Error("Brother must not match Brothers")
	}
	// But a genuine superset still matches.
	if got, ok := pickSeries([]itvdb.Series{{ID: "9", Name: "The Brothers Sun"}}, "Brothers", 0); !ok || got.ID != "9" {
		t.Errorf("a name containing the search as whole words should match, ok=%v", ok)
	}
}

func TestMergeSeriesKeepsFirstOccurrence(t *testing.T) {
	a := []itvdb.Series{{ID: "1", Name: "A"}, {ID: "2", Name: "B"}}
	b := []itvdb.Series{{ID: "2", Name: "B"}, {ID: "3", Name: "C"}}
	got := mergeSeries(a, b)
	if len(got) != 3 || got[0].ID != "1" || got[1].ID != "2" || got[2].ID != "3" {
		t.Errorf("merge = %+v, want 1,2,3 in order with no repeat", got)
	}
	if n := len(mergeSeries(nil, nil)); n != 0 {
		t.Errorf("empty merge produced %d", n)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// --- resolving the series by id ---

// makeServerTwoTombRaiders mirrors the real ambiguity: TheTVDB lists two shows
// whose titles normalise to "tomb raider", and a search for that name ranks the
// 2026 series first. Only the id separates them. Every request is counted so a
// test can assert which endpoints were consulted.
func makeServerTwoTombRaiders(hits map[string]int) *httptest.Server {
	series := map[string]struct{ name, slug, poster string }{
		"450360": {"Tomb Raider", "tomb-raider", "https://artworks.thetvdb.com/450360.jpg"},
		"409591": {"Tomb Raider: The Legend of Lara Croft", "tomb-raider-anime", "https://artworks.thetvdb.com/409591.jpg"},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch {
		case r.URL.Path == "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "jwt"}, "status": "success",
			})
		case r.URL.Path == "/v4/search":
			// Relevance puts the 2026 series first, which is the whole problem.
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"tvdb_id": "450360", "name": "Tomb Raider", "slug": "tomb-raider",
						"originalLanguage": "eng", "image_url": series["450360"].poster},
					{"tvdb_id": "409591", "name": "Tomb Raider: The Legend of Lara Croft",
						"slug": "tomb-raider-anime", "originalLanguage": "eng"},
				},
				"status": "success",
			})
		case strings.HasSuffix(r.URL.Path, "/extended"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v4/series/"), "/extended")
			s, ok := series[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"name": s.name, "slug": s.slug, "image": s.poster,
					"originalLanguage": "eng", "originalCountry": "usa",
					"originalNetwork": map[string]any{"name": "Netflix"},
					"firstAired":      "2024-10-10",
					"status":          map[string]any{"name": "Ended"},
					"genres":          []map[string]any{{"name": "Animation"}},
				},
				"status": "success",
			})
		case strings.HasSuffix(r.URL.Path, "/episodes/official"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v4/series/"), "/episodes/official")
			if _, ok := series[id]; !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"episodes": []map[string]any{
					{"id": 1, "seasonNumber": 1, "number": 1,
						"name": "A Single Step for series " + id, "aired": "2024-10-10"},
				}},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestAnnotatePrefersTheEntrysID: series_gaps computed the gap for a specific
// series and discover carries that identity onto the release it found. A search
// for the release's parsed name would pick the other show and enrich the card
// with its poster, its link and its episode titles — confidently wrong.
func TestAnnotatePrefersTheEntrysID(t *testing.T) {
	hits := map[string]int{}
	srv := makeServerTwoTombRaiders(hits)
	defer srv.Close()
	p := makePlugin(t, srv)

	e := entry.New("Tomb.Raider.S01E01.1080p.WEB-DL", "http://x.com/a")
	e.Set("tvdb_id", "409591")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("tvdb_id"); v != "409591" {
		t.Errorf("tvdb_id = %q, want the id the entry arrived with", v)
	}
	if v := e.GetString("tvdb_slug"); v != "tomb-raider-anime" {
		t.Errorf("tvdb_slug = %q, want tomb-raider-anime", v)
	}
	if v := e.GetString("title"); v != "Tomb Raider: The Legend of Lara Croft" {
		t.Errorf("title = %q, want the show the id names", v)
	}
	if v := e.GetString(entry.FieldVideoPoster); v != "https://artworks.thetvdb.com/409591.jpg" {
		t.Errorf("video_poster = %q, want the 409591 poster", v)
	}
	if v := e.GetString(entry.FieldSeriesEpisodeTitle); v != "A Single Step for series 409591" {
		t.Errorf("series_episode_title = %q, want the episode of 409591", v)
	}
	if !e.GetBool(entry.FieldEnriched) {
		t.Error("entry should be enriched")
	}
	// Not just the right answer — the search is not consulted at all, so an
	// ambiguous name cannot influence the outcome.
	if n := hits["/v4/search"]; n != 0 {
		t.Errorf("search called %d times, want 0 when the entry carries an id", n)
	}
}

// TestAnnotateFallsBackToSearchWhenTheIDIsUnknown: an id TheTVDB does not
// answer for must not cost the entry its enrichment — the name search is still
// there, and behaves as it always did.
func TestAnnotateFallsBackToSearchWhenTheIDIsUnknown(t *testing.T) {
	hits := map[string]int{}
	srv := makeServerTwoTombRaiders(hits)
	defer srv.Close()
	p := makePlugin(t, srv)

	e := entry.New("Tomb.Raider.S01E01.1080p.WEB-DL", "http://x.com/a")
	e.Set("tvdb_id", "999999") // no such series on this server
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	if n := hits["/v4/search"]; n != 1 {
		t.Errorf("search called %d times, want 1 as the fallback", n)
	}
	if v := e.GetString("tvdb_id"); v != "450360" {
		t.Errorf("tvdb_id = %q, want the searched show's id", v)
	}
	if !e.GetBool(entry.FieldEnriched) {
		t.Error("entry should still be enriched via the search fallback")
	}
}

// TestAnnotateWithoutAnIDSearchesAsBefore pins the path every other pipeline
// takes: nothing upstream of metainfo_tvdb has to supply an id.
func TestAnnotateWithoutAnIDSearchesAsBefore(t *testing.T) {
	hits := map[string]int{}
	srv := makeServerTwoTombRaiders(hits)
	defer srv.Close()
	p := makePlugin(t, srv)

	e := entry.New("Tomb.Raider.S01E01.1080p.WEB-DL", "http://x.com/a")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if n := hits["/v4/search"]; n != 1 {
		t.Errorf("search called %d times, want 1", n)
	}
	if v := e.GetString("tvdb_id"); v != "450360" {
		t.Errorf("tvdb_id = %q, want the searched show's id", v)
	}
}
