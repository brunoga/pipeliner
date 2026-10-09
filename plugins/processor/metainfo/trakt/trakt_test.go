package trakt

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
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

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
		Rating   float64  `json:"video_rating"`
		Votes    int      `json:"video_votes"`
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
		w.Write(body)
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

	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}

	if v := e.GetInt("trakt_id"); v != 1 {
		t.Errorf("trakt_id: got %d, want 1", v)
	}
	if v := e.GetString("title"); v != "Breaking Bad" {
		t.Errorf("trakt_title: got %q, want %q", v, "Breaking Bad")
	}
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q, want %q", v, entry.MediaTypeSeries)
	}
	if v := e.GetInt("video_year"); v != 2008 {
		t.Errorf("trakt_year: got %d, want 2008", v)
	}
	if v := e.GetString("video_imdb_id"); v != "tt0903747" {
		t.Errorf("trakt_imdb_id: got %q", v)
	}
	if v := e.GetInt("trakt_tvdb_id"); v != 81189 {
		t.Errorf("trakt_tvdb_id: got %d, want 81189", v)
	}
	if g, _ := e.Get("video_genres"); g == nil {
		t.Error("genres should be set")
	}
	if v := e.GetString("description"); v == "" {
		t.Error("trakt_overview should be set")
	}
}

func TestEnrichedSetOnSuccess(t *testing.T) {
	srv := mockServer(t, searchResponse("Breaking Bad", 2008, 1, 81189, 9.4, []string{"Drama"}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if !e.GetBool("enriched") {
		t.Error("enriched should be true when Trakt finds the show")
	}
}

func TestEnrichedNotSetOnNoResults(t *testing.T) {
	srv := mockServer(t, []byte("[]"))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if e.GetBool("enriched") {
		t.Error("enriched should not be set when Trakt returns no results")
	}
}

func TestAnnotateNonParseableTitle(t *testing.T) {
	srv := mockServer(t, []byte("[]"))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Just A Random Article", "http://example.com/1")

	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Check Fields directly: GetString("title") would fall back to the
	// raw struct e.Title; we want to verify Fields was not populated.
	if _, ok := e.Fields["title"]; ok {
		t.Errorf("non-parseable title should not set Fields[\"title\"], got %q", e.Fields["title"])
	}
}

func TestAnnotateNoResults(t *testing.T) {
	srv := mockServer(t, []byte("[]"))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Unknown.Show.S01E01.720p", "http://example.com/1")

	if err := p.annotate(context.Background(), tc(), e); err != nil {
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
		Rating   float64  `json:"video_rating"`
		Votes    int      `json:"video_votes"`
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

	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetInt("trakt_id"); v != 42 {
		t.Errorf("trakt_id: got %d, want 42", v)
	}
	if v := e.GetString("video_imdb_id"); v != "tt1375666" {
		t.Errorf("trakt_imdb_id: got %q", v)
	}
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeMovie {
		t.Errorf("media_type: got %q, want %q", v, entry.MediaTypeMovie)
	}
	// Movies don't have tvdb_id.
	if v := e.GetInt("trakt_tvdb_id"); v != 0 {
		t.Error("movies should not set trakt_tvdb_id")
	}
}

func TestEmptyResultNotCached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	p.annotate(context.Background(), tc(), e)
	p.annotate(context.Background(), tc(), e)

	if callCount < 2 {
		t.Errorf("empty result should not be cached; API called %d times, want ≥2", callCount)
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

func TestAnnotateExtendedFields(t *testing.T) {
	// Confirms extended=full fields (runtime/language/country/trailer/homepage/
	// certification + show-only network/status/first_aired) land on the entry
	// via the standard video_/series_ fields.
	body := []byte(`[{
		"type":"show","score":1000,"show":{
			"title":"Breaking Bad","year":2008,
			"ids":{"trakt":1,"slug":"breaking-bad","imdb":"tt0903747","tmdb":1396,"tvdb":81189},
			"overview":"A chemistry teacher.",
			"rating":9.4,"votes":500000,"genres":["drama"],
			"runtime":47,"country":"us","language":"en",
			"trailer":"https://youtube.com/watch?v=bb",
			"homepage":"https://breakingbad.example",
			"certification":"TV-MA",
			"network":"AMC","status":"ended",
			"first_aired":"2008-01-20T05:00:00.000Z"
		}
	}]`)
	srv := mockServer(t, body)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		field string
		want  any
	}{
		{entry.FieldVideoRuntime, 47},
		{entry.FieldVideoLanguage, "English"},
		{entry.FieldVideoCountry, "United States"},
		{entry.FieldVideoContentRating, "TV-MA"},
		{entry.FieldVideoHomepage, "https://breakingbad.example"},
		{entry.FieldSeriesNetwork, "AMC"},
		{entry.FieldSeriesStatus, "ended"},
	}
	for _, c := range checks {
		got, _ := e.Get(c.field)
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.field, got, c.want)
		}
	}
	trailers, _ := e.Get(entry.FieldVideoTrailers)
	urls, _ := trailers.([]string)
	if len(urls) != 1 || urls[0] != "https://youtube.com/watch?v=bb" {
		t.Errorf("%s: got %v", entry.FieldVideoTrailers, trailers)
	}
}

func TestPickItemPrefersExactTitle(t *testing.T) {
	rs := []itrakt.Item{
		{Title: "The Office (US) Extras"},
		{Title: "The Office"},
	}
	if got := pickItem(rs, "The Office", 0); got.Title != "The Office" {
		t.Errorf("exact title should win, got %q", got.Title)
	}
	if got := pickItem(rs, "Office Space", 0); got.Title != "The Office (US) Extras" {
		t.Errorf("no exact match should fall back to results[0], got %q", got.Title)
	}
}

// --- identity before search ---

// movieSearchResponse encodes a Trakt movie search body with the fields
// resolution depends on.
func movieSearchResponse(items ...[2]any) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
		IMDB  string `json:"imdb"`
		TMDB  int    `json:"tmdb"`
	}
	type movie struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	type wrapper struct {
		Type  string  `json:"type"`
		Movie movie   `json:"movie"`
		Score float64 `json:"score"`
	}
	out := make([]wrapper, 0, len(items))
	for _, it := range items {
		title, year := it[0].(string), it[1].(int)
		out = append(out, wrapper{
			Type:  "movie",
			Score: 1000,
			Movie: movie{Title: title, Year: year, IDs: ids{Trakt: year, TMDB: year}},
		})
	}
	b, _ := json.Marshal(out)
	return b
}

// countingServer records the request paths it served, so a test can assert
// which question was asked.
func countingServer(t *testing.T, bodyFor func(path string) []byte, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if b := bodyFor(r.URL.Path); b != nil {
			w.Write(b)
			return
		}
		w.Write([]byte("[]"))
	}))
}

// TestAnnotateResolvesByTheEntrysID: a pipeline that ran trakt_list or another
// metainfo plugin first already knows which item this is. Resolving it again by
// name throws that away, and Trakt answers a name with whatever ranks highest.
func TestAnnotateResolvesByTheEntrysID(t *testing.T) {
	var paths []string
	srv := countingServer(t, func(path string) []byte {
		if path == "/search/tvdb/81189" {
			return searchResponse("Breaking Bad", 2008, 1, 81189, 9.4, []string{"drama"})
		}
		return nil
	}, &paths)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1")
	e.Set("tvdb_id", "81189")

	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetInt("trakt_id"); v != 1 {
		t.Errorf("trakt_id = %d, want 1", v)
	}
	if len(paths) != 1 || paths[0] != "/search/tvdb/81189" {
		t.Errorf("requests = %v, want a single id lookup", paths)
	}
}

// TestAnnotateFallsBackToTheNameSearch: an id Trakt has no item for must not
// cost the entry its enrichment.
func TestAnnotateFallsBackToTheNameSearch(t *testing.T) {
	var paths []string
	srv := countingServer(t, func(path string) []byte {
		if path == "/search/show" {
			return searchResponse("Breaking Bad", 2008, 1, 81189, 9.4, []string{"drama"})
		}
		return nil // the id lookup answers with an empty array
	}, &paths)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1")
	e.Set("tvdb_id", "999999")

	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetInt("trakt_id"); v != 1 {
		t.Errorf("trakt_id = %d, want 1 via the search fallback", v)
	}
	if len(paths) != 2 || paths[0] != "/search/tvdb/999999" || paths[1] != "/search/show" {
		t.Errorf("requests = %v, want the id lookup then the search", paths)
	}
}

// TestAnnotateWithoutAnIDSearchesByName pins the unchanged path.
func TestAnnotateWithoutAnIDSearchesByName(t *testing.T) {
	var paths []string
	srv := countingServer(t, func(path string) []byte {
		if path == "/search/show" {
			return searchResponse("Breaking Bad", 2008, 1, 81189, 9.4, []string{"drama"})
		}
		return nil
	}, &paths)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1")
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/search/show" {
		t.Errorf("requests = %v, want a single name search", paths)
	}
}

// TestAnnotateIDLookupIsCached: the mapping never changes, so repeating it
// would be pure waste.
func TestAnnotateIDLookupIsCached(t *testing.T) {
	var paths []string
	srv := countingServer(t, func(path string) []byte {
		if path == "/search/tvdb/81189" {
			return searchResponse("Breaking Bad", 2008, 1, 81189, 9.4, []string{"drama"})
		}
		return nil
	}, &paths)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "shows"})
	for i := 0; i < 3; i++ {
		e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1")
		e.Set("tvdb_id", "81189")
		if err := p.annotate(context.Background(), tc(), e); err != nil {
			t.Fatal(err)
		}
	}
	if len(paths) != 1 {
		t.Errorf("requests = %v, want one lookup for three entries", paths)
	}
}

// TestPickItemUsesTheYear: two films share the title and Trakt ranks the
// newer one first, which is the only thing the old code looked at.
func TestPickItemUsesTheYear(t *testing.T) {
	rs := []itrakt.Item{
		{Title: "Michael", Year: 2026},
		{Title: "Michael", Year: 1996},
	}
	if got := pickItem(rs, "Michael", 1996); got.Year != 1996 {
		t.Errorf("year 1996 asked for, got %d", got.Year)
	}
	if got := pickItem(rs, "Michael", 2026); got.Year != 2026 {
		t.Errorf("year 2026 asked for, got %d", got.Year)
	}
	// Off-by-one across regional windows is the same film.
	if got := pickItem(rs, "Michael", 1997); got.Year != 1996 {
		t.Errorf("an off-by-one year should still pick 1996, got %d", got.Year)
	}
	// No year: relevance order stands, as before.
	if got := pickItem(rs, "Michael", 0); got.Year != 2026 {
		t.Errorf("no year should keep relevance order, got %d", got.Year)
	}
}

// TestPickItemYearIsAPreferenceNotAFilter: a release year is often absent and
// occasionally wrong, so a title match with no compatible year still beats
// falling through to relevance.
func TestPickItemYearIsAPreferenceNotAFilter(t *testing.T) {
	rs := []itrakt.Item{
		{Title: "Something Else", Year: 2010},
		{Title: "Michael", Year: 1996},
	}
	if got := pickItem(rs, "Michael", 2020); got.Title != "Michael" {
		t.Errorf("title match should still win, got %q", got.Title)
	}
}

// TestAnnotateMoviePrefersTheRightYear is the end-to-end of the above: the
// release names 1996 and the search ranks 2026 first.
func TestAnnotateMoviePrefersTheRightYear(t *testing.T) {
	var paths []string
	srv := countingServer(t, func(path string) []byte {
		if path == "/search/movie" {
			return movieSearchResponse([2]any{"Michael", 2026}, [2]any{"Michael", 1996})
		}
		return nil
	}, &paths)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makePlugin(t, map[string]any{"client_id": "key", "type": "movies"})
	e := entry.New("Michael.1996.1080p.BluRay.x264", "http://example.com/1")
	if err := p.annotate(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetInt(entry.FieldVideoYear); v != 1996 {
		t.Errorf("video_year = %d, want 1996 (the film the release names)", v)
	}
}
