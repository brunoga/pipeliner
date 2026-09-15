package tmdb

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
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
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"id": 27205, "title": "Inception",
						"release_date": "2010-07-16", "popularity": 99.5,
						"vote_average": 8.8, "overview": "A thief...",
						"vote_count": 35000, "poster_path": "/inception.jpg",
						"original_language": "en",
					},
				},
				"page": 1, "total_results": 1,
			})
		case "/3/movie/27205":
			json.NewEncoder(w).Encode(map[string]any{
				"id": 27205, "title": "Inception", "release_date": "2010-07-16",
				"runtime": 148, "tagline": "Your mind is the scene.",
				"homepage": "https://www.warnerbros.com/movies/inception",
				"imdb_id":  "tt1375666", "original_language": "en",
				"vote_count": 35000, "poster_path": "/inception.jpg",
				"genres":               []map[string]any{{"id": 28, "name": "Action"}, {"id": 878, "name": "Science Fiction"}},
				"production_countries": []map[string]any{{"iso_3166_1": "US", "name": "United States of America"}},
				"credits": map[string]any{
					"cast": []map[string]any{
						{"name": "Leonardo DiCaprio", "order": 0},
						{"name": "Joseph Gordon-Levitt", "order": 1},
					},
				},
				"videos": map[string]any{
					"results": []map[string]any{
						{"key": "YoHD9XEInc0", "site": "YouTube", "type": "Trailer"},
					},
				},
				"release_dates": map[string]any{
					"results": []map[string]any{
						{
							"iso_3166_1": "US",
							"release_dates": []map[string]any{
								{"certification": "PG-13", "type": 3},
							},
						},
					},
				},
				"alternative_titles": map[string]any{
					"titles": []map[string]any{
						{"title": "Inception: The IMAX Experience"},
					},
				},
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
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetString("title"); v != "Inception" {
		t.Errorf("tmdb_title: got %q", v)
	}
	if v := e.GetInt("tmdb_id"); v != 27205 {
		t.Errorf("tmdb_id: got %d", v)
	}
	if v := e.GetInt("video_runtime"); v != 148 {
		t.Errorf("tmdb_runtime: got %d", v)
	}
	if v := e.GetString("video_imdb_id"); v != "tt1375666" {
		t.Errorf("tmdb_imdb_id: got %q", v)
	}
	genresRaw, _ := e.Get("video_genres")
	genreSlice, _ := genresRaw.([]string)
	if len(genreSlice) != 2 || genreSlice[0] != "Action" || genreSlice[1] != "Science Fiction" {
		t.Errorf("genres: got %v", genresRaw)
	}
	if v := e.GetString("video_poster"); v != "https://image.tmdb.org/t/p/w500/inception.jpg" {
		t.Errorf("video_poster: got %q", v)
	}
	if v := e.GetString("video_language"); v != "English" {
		t.Errorf("video_language: got %q", v)
	}
	if v := e.GetString("video_country"); v != "United States of America" {
		t.Errorf("video_country: got %q", v)
	}
	if v := e.GetString("video_content_rating"); v != "PG-13" {
		t.Errorf("video_content_rating: got %q", v)
	}
	castRaw, _ := e.Get("video_cast")
	castSlice, _ := castRaw.([]string)
	if len(castSlice) != 2 || castSlice[0] != "Leonardo DiCaprio" {
		t.Errorf("video_cast: got %v", castRaw)
	}
	trailersRaw, _ := e.Get("video_trailers")
	trailerSlice, _ := trailersRaw.([]string)
	if len(trailerSlice) != 1 || trailerSlice[0] != "https://www.youtube.com/watch?v=YoHD9XEInc0" {
		t.Errorf("video_trailers: got %v", trailersRaw)
	}
	if v := e.GetInt("video_votes"); v != 35000 {
		t.Errorf("video_votes: got %d", v)
	}
	if v := e.GetString("video_homepage"); v != "https://www.warnerbros.com/movies/inception" {
		t.Errorf("video_homepage: got %q", v)
	}
	aliasesRaw, _ := e.Get("video_aliases")
	aliasSlice, _ := aliasesRaw.([]string)
	if len(aliasSlice) != 1 || aliasSlice[0] != "Inception: The IMAX Experience" {
		t.Errorf("video_aliases: got %v", aliasesRaw)
	}
}

func TestEnrichedSetOnSuccess(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	p := &tmdbPlugin{client: c}

	e := entry.New("Inception.2010.1080p.BluRay", "http://x.com/a")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.GetBool("enriched") {
		t.Error("enriched should be true when TMDb finds the movie")
	}
}

func TestEmptyResultNotCached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/search/movie" {
			callCount++
			json.NewEncoder(w).Encode(map[string]any{"results": []any{}, "total_results": 0})
		}
	}))
	defer srv.Close()

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	p := &tmdbPlugin{
		client: c,
		cache:  cache.NewPersistent[[]itmdb.Movie](time.Hour, db.Bucket("test")),
	}

	e := entry.New("Inception.2010.1080p.BluRay", "http://x.com/a")
	p.annotate(context.Background(), makeCtx(), e)
	p.annotate(context.Background(), makeCtx(), e)

	// Each Annotate makes 2 API calls (year search + year-less retry).
	// If empty results were cached, the second Annotate would skip the API entirely.
	if callCount < 3 {
		t.Errorf("empty result should not be cached; API called %d times, want ≥3", callCount)
	}
}

func TestEnrichedNotSetOnNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie":
			json.NewEncoder(w).Encode(map[string]any{"results": []any{}, "total_results": 0})
		}
	}))
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	p := &tmdbPlugin{client: c}

	e := entry.New("Inception.2010.1080p.BluRay", "http://x.com/a")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.GetBool("enriched") {
		t.Error("enriched should not be set when TMDb returns no results")
	}
}

func TestAnnotateNonMovie(t *testing.T) {
	srv := makeServer()
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"

	p := &tmdbPlugin{client: c}

	e := entry.New("Show.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	// Check Fields directly: GetString("title") would fall back to the
	// raw struct e.Title (set by entry.New). What we want to verify
	// here is that tmdb did not populate Fields["title"].
	if _, ok := e.Fields["title"]; ok {
		t.Errorf("Fields[\"title\"] should not be set for series, got %q", e.Fields["title"])
	}
}

// TestAnnotateByTraktTMDBID verifies that when an entry carries trakt_tmdb_id
// the plugin fetches the movie by ID directly and never calls the search endpoint.
func TestAnnotateByTraktTMDBID(t *testing.T) {
	searchCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie":
			searchCalled = true
			json.NewEncoder(w).Encode(map[string]any{"results": []any{}, "total_results": 0})
		case "/3/movie/27205":
			json.NewEncoder(w).Encode(map[string]any{
				"id": 27205, "title": "Michael", "release_date": "2026-03-14",
				"overview": "A film about Michael Jackson.", "popularity": 50.0,
				"vote_average": 7.5, "vote_count": 1000, "poster_path": "",
				"original_language": "en", "original_title": "Michael",
				"runtime": 132, "tagline": "", "imdb_id": "tt12345678",
				"genres":               []map[string]any{{"id": 18, "name": "Drama"}},
				"production_countries": []map[string]any{},
				"credits":              map[string]any{"cast": []any{}},
				"videos":               map[string]any{"results": []any{}},
				"release_dates":        map[string]any{"results": []any{}},
				"alternative_titles":   map[string]any{"titles": []any{}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	p := &tmdbPlugin{client: c}

	// Entry from trakt_list: plain title, year stored in trakt_tmdb_id.
	e := entry.New("Michael", "https://trakt.tv/movies/michael-2026")
	e.Set("trakt_tmdb_id", 27205)
	e.Set(entry.FieldVideoYear, 2026)

	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	if searchCalled {
		t.Error("search endpoint should not be called when trakt_tmdb_id is present")
	}
	if v := e.GetInt("tmdb_id"); v != 27205 {
		t.Errorf("tmdb_id: got %d, want 27205", v)
	}
	if v := e.GetInt("video_year"); v != 2026 {
		t.Errorf("video_year: got %d, want 2026", v)
	}
	if v := e.GetString("video_imdb_id"); v != "tt12345678" {
		t.Errorf("video_imdb_id: got %q", v)
	}
	if !e.GetBool("enriched") {
		t.Error("enriched should be true")
	}
}

// TestTraktYearUsedAsSearchHint verifies that when an entry has no year in its
// title but carries trakt_year, that year is passed to the TMDb search so that
// a same-name film from a different decade is not returned instead.
func TestTraktYearUsedAsSearchHint(t *testing.T) {
	var searchedYear string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie":
			searchedYear = r.URL.Query().Get("year")
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"id": 27205, "title": "Michael", "release_date": "2026-03-14",
						"popularity": 50.0, "vote_average": 7.5, "vote_count": 1000,
						"poster_path": "", "original_title": "Michael",
					},
				},
				"total_results": 1,
			})
		case "/3/movie/27205":
			json.NewEncoder(w).Encode(map[string]any{
				"id": 27205, "title": "Michael", "release_date": "2026-03-14",
				"runtime": 132, "tagline": "", "imdb_id": "tt12345678",
				"original_language": "en", "genres": []any{},
				"production_countries": []any{}, "credits": map[string]any{"cast": []any{}},
				"videos":             map[string]any{"results": []any{}},
				"release_dates":      map[string]any{"results": []any{}},
				"alternative_titles": map[string]any{"titles": []any{}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	p := &tmdbPlugin{client: c}

	// Plain title with no year, year provided via trakt_year only.
	e := entry.New("Michael", "https://trakt.tv/movies/michael-2026")
	e.Set(entry.FieldVideoYear, 2026)

	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	if searchedYear != "2026" {
		t.Errorf("TMDb search year: got %q, want %q", searchedYear, "2026")
	}
	if v := e.GetInt("tmdb_id"); v != 27205 {
		t.Errorf("tmdb_id: got %d, want 27205", v)
	}
}

// TestAnnotateByIDDetailCached verifies that when two entries carry the same
// trakt_tmdb_id, the second annotate call uses the cached detail and does not
// make a second GetMovie network request.
func TestAnnotateByIDDetailCached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/movie/27205":
			callCount++
			json.NewEncoder(w).Encode(map[string]any{
				"id": 27205, "title": "Inception", "release_date": "2010-07-16",
				"overview": "A thief...", "popularity": 99.5,
				"vote_average": 8.8, "vote_count": 35000, "poster_path": "/inception.jpg",
				"original_language": "en", "original_title": "Inception",
				"runtime": 148, "tagline": "Your mind is the scene.",
				"imdb_id":              "tt1375666",
				"genres":               []map[string]any{{"id": 28, "name": "Action"}},
				"production_countries": []map[string]any{},
				"credits":              map[string]any{"cast": []any{}},
				"videos":               map[string]any{"results": []any{}},
				"release_dates":        map[string]any{"results": []any{}},
				"alternative_titles":   map[string]any{"titles": []any{}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	p := &tmdbPlugin{
		client:      c,
		cache:       cache.NewPersistent[[]itmdb.Movie](time.Hour, db.Bucket("search")),
		detailCache: cache.NewPersistent[*itmdb.MovieDetail](time.Hour, db.Bucket("detail")),
	}

	for i := range 2 {
		e := entry.New("Michael", "https://trakt.tv/movies/michael-2026")
		e.Set("trakt_tmdb_id", 27205)
		if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
			t.Fatalf("annotate %d: %v", i, err)
		}
	}
	if callCount != 1 {
		t.Errorf("GetMovie called %d times, want 1 (second should use detail cache)", callCount)
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("metainfo_tmdb")
	if !ok {
		t.Fatal("metainfo_tmdb not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("phase: got %v", d.Role)
	}
}

// TestYearlessRetryFiltersNonMatchingTitles verifies that when the year-based
// search returns no results and the yearless retry fires, only results whose
// title fuzzy-matches the searched title are kept. This prevents cases like
// "Mother" (2025) being enriched as "Mother's Day" (2016).
func TestYearlessRetryFiltersNonMatchingTitles(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		year := r.URL.Query().Get("year")
		switch r.URL.Path {
		case "/3/search/movie":
			if year != "" {
				// First call (with year): no results — film not on TMDb yet.
				json.NewEncoder(w).Encode(map[string]any{"results": []any{}, "page": 1, "total_results": 0})
			} else {
				// Yearless retry: return "Mother's Day" as the top popularity result.
				json.NewEncoder(w).Encode(map[string]any{
					"results": []map[string]any{
						{"id": 9999, "title": "Mother's Day", "release_date": "2016-05-06",
							"popularity": 120.0, "vote_average": 5.8, "vote_count": 1000},
					},
					"page": 1, "total_results": 1,
				})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	p := &tmdbPlugin{
		client:      c,
		cache:       cache.NewPersistent[[]itmdb.Movie](time.Hour, db.Bucket("cache_tmdb_test")),
		detailCache: cache.NewPersistent[*itmdb.MovieDetail](time.Hour, db.Bucket("cache_tmdb_detail_test")),
	}

	e := entry.New("Mother.2025.1080p.WEBRip", "http://x.com/1")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}

	// The entry must NOT be enriched — "Mother's Day" is not a title match for "Mother".
	if e.GetString("title") == "Mother's Day" {
		t.Error("yearless retry must not accept a non-matching title like Mother's Day for Mother")
	}
	if e.GetInt("tmdb_id") != 0 {
		t.Errorf("tmdb_id should not be set when no valid title match found, got %d", e.GetInt("tmdb_id"))
	}
}

// TestPickByYear covers the result-selection helper directly.
func TestPickBest(t *testing.T) {
	rs := []itmdb.Movie{
		{ID: 78, Title: "Halloween", ReleaseDate: "1978-10-25"},   // popular original, re-released 2018
		{ID: 2018, Title: "Halloween", ReleaseDate: "2018-10-19"}, // the year we actually asked for
	}
	if got := pickBest(rs, "Halloween", 2018); got.ID != 2018 {
		t.Errorf("year 2018 → want id 2018, got %d", got.ID)
	}
	if got := pickBest(rs, "Halloween", 1978); got.ID != 78 {
		t.Errorf("year 1978 → want id 78, got %d", got.ID)
	}
	if got := pickBest(rs, "Halloween", 0); got.ID != 78 {
		t.Errorf("year 0 → want first (id 78), got %d", got.ID)
	}
	if got := pickBest(rs, "Halloween", 1999); got.ID != 78 {
		t.Errorf("no year match → want first (id 78), got %d", got.ID)
	}

	// X2 case: a same-year companion piece sorts first (and is even more
	// "popular" in the raw ranking), but the actual film's title matches
	// exactly and must win.
	x2 := []itmdb.Movie{
		{ID: 100, Title: "X2 Global Webcast Highlights", ReleaseDate: "2003-06-01", Popularity: 50},
		{ID: 200, Title: "X2", ReleaseDate: "2003-04-24", Popularity: 30},
	}
	if got := pickBest(x2, "X2", 2003); got.ID != 200 {
		t.Errorf("X2 → want the feature film (id 200), got %d", got.ID)
	}

	// When no candidate matches the title exactly, popularity breaks the tie so
	// the real film still beats a low-signal companion piece.
	inexact := []itmdb.Movie{
		{ID: 100, Title: "X2 Global Webcast Highlights", ReleaseDate: "2003-06-01", Popularity: 5},
		{ID: 200, Title: "X2: X-Men United", ReleaseDate: "2003-04-24", Popularity: 80},
	}
	if got := pickBest(inexact, "X2", 2003); got.ID != 200 {
		t.Errorf("inexact titles → want the more popular film (id 200), got %d", got.ID)
	}
}

// TestAnnotatePrefersMatchingYear is the Halloween case: a release named
// "Halloween-2018" must enrich as the 2018 film, not the more popular 1978
// original that TMDb's year filter also returns (via a 2018 re-release).
func TestAnnotatePrefersMatchingYear(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie":
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"id": 78, "title": "Halloween", "release_date": "1978-10-25", "popularity": 90.0},
				{"id": 2018, "title": "Halloween", "release_date": "2018-10-19", "popularity": 40.0},
			}})
		case "/3/movie/2018":
			json.NewEncoder(w).Encode(map[string]any{"id": 2018, "title": "Halloween", "release_date": "2018-10-19", "runtime": 106})
		case "/3/movie/78":
			json.NewEncoder(w).Encode(map[string]any{"id": 78, "title": "Halloween", "release_date": "1978-10-25", "runtime": 91})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	p := &tmdbPlugin{client: c}

	e := entry.New("Halloween-2018-1080p UHD BluRay AV1", "http://x/a")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetInt("tmdb_id"); v != 2018 {
		t.Errorf("picked the wrong film: tmdb_id=%d, want 2018", v)
	}
}

// TestAnnotatePrefersTitleMatch is the X2 case: a release named "X2 2003" must
// enrich as the feature film, not a same-year companion piece ("X2 Global
// Webcast Highlights") that TMDb's popularity-ranked search returns first.
func TestAnnotatePrefersTitleMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie":
			// The companion piece sorts first and outranks the film on popularity.
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"id": 100, "title": "X2 Global Webcast Highlights", "release_date": "2003-06-01", "popularity": 50.0},
				{"id": 200, "title": "X2", "release_date": "2003-04-24", "popularity": 30.0},
			}})
		case "/3/movie/200":
			json.NewEncoder(w).Encode(map[string]any{"id": 200, "title": "X2", "release_date": "2003-04-24", "runtime": 134})
		case "/3/movie/100":
			json.NewEncoder(w).Encode(map[string]any{"id": 100, "title": "X2 Global Webcast Highlights", "release_date": "2003-06-01", "runtime": 17})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := itmdb.New("test-key")
	c.BaseURL = srv.URL + "/3"
	p := &tmdbPlugin{client: c}

	e := entry.New("X2 2003 2160p DSNP WEB-DL DTS-HD MA 5 1 HDR10Plus H 265-Kitsune", "http://x/b")
	if err := p.annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetInt("tmdb_id"); v != 200 {
		t.Errorf("picked the wrong film: tmdb_id=%d, want 200 (the feature film)", v)
	}
}

// TestFetchDetailStaleFallback proves a transient TMDb detail-fetch failure
// does not discard genres already fetched: fetchDetail serves the stale cached
// detail instead of degrading to the genre-less search result.
func TestFetchDetailStaleFallback(t *testing.T) {
	failDetail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie":
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{ //nolint:errcheck
				{"id": 27205, "title": "Inception", "release_date": "2010-07-16"},
			}})
		case "/3/movie/27205":
			if failDetail {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"id": 27205, "title": "Inception", "release_date": "2010-07-16", "runtime": 148,
				"genres": []map[string]any{{"id": 28, "name": "Action"}, {"id": 878, "name": "Science Fiction"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := itmdb.New("k")
	c.BaseURL = srv.URL + "/3"
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	p := &tmdbPlugin{
		client:      c,
		cache:       cache.NewPersistent[[]itmdb.Movie](20*time.Millisecond, db.Bucket("s")),
		detailCache: cache.NewPersistent[*itmdb.MovieDetail](20*time.Millisecond, db.Bucket("d")),
	}

	genresOf := func(e *entry.Entry) []string {
		v, _ := e.Get("video_genres")
		gs, _ := v.([]string)
		return gs
	}

	// First run: detail succeeds, genres cached.
	e1 := entry.New("Inception 2010 1080p BluRay", "http://x/a")
	if err := p.annotate(context.Background(), makeCtx(), e1); err != nil {
		t.Fatal(err)
	}
	if len(genresOf(e1)) == 0 {
		t.Fatal("first run should populate genres from the detail endpoint")
	}

	// Expire the caches, then break the detail endpoint.
	time.Sleep(30 * time.Millisecond)
	failDetail = true

	// Second run: detail fetch fails, but the stale cache preserves genres.
	e2 := entry.New("Inception 2010 2160p BluRay", "http://x/b")
	if err := p.annotate(context.Background(), makeCtx(), e2); err != nil {
		t.Fatal(err)
	}
	if len(genresOf(e2)) == 0 {
		t.Error("stale-cache fallback should preserve genres when the detail fetch fails")
	}
}
