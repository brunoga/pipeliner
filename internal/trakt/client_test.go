package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func trendingResponse(titles []string) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
		IMDB  string `json:"imdb"`
		TMDB  int    `json:"tmdb"`
		TVDB  int    `json:"tvdb"`
	}
	type show struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	type item struct {
		Watchers int  `json:"watchers"`
		Show     show `json:"show"`
	}
	var items []item
	for i, t := range titles {
		items = append(items, item{
			Watchers: 100 - i,
			Show: show{
				Title: t,
				Year:  2020 + i,
				IDs:   ids{Trakt: i + 1, Slug: "slug-" + t},
			},
		})
	}
	b, _ := json.Marshal(items)
	return b
}

func popularResponse(titles []string) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
	}
	type item struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	var items []item
	for i, t := range titles {
		items = append(items, item{
			Title: t,
			Year:  2020 + i,
			IDs:   ids{Trakt: i + 1, Slug: "slug-" + t},
		})
	}
	b, _ := json.Marshal(items)
	return b
}

func watchlistResponse(titles []string) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
	}
	type show struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	type item struct {
		ListedAt string `json:"listed_at"`
		Type     string `json:"type"`
		Show     show   `json:"show"`
	}
	var items []item
	for i, t := range titles {
		items = append(items, item{
			ListedAt: "2024-01-01",
			Type:     "show",
			Show:     show{Title: t, Year: 2020 + i, IDs: ids{Trakt: i + 1}},
		})
	}
	b, _ := json.Marshal(items)
	return b
}

func ratingsResponse(titles []string, ratings []int) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
	}
	type show struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	type item struct {
		Rating int    `json:"rating"`
		RatedAt string `json:"rated_at"`
		Show   show   `json:"show"`
	}
	var items []item
	for i, t := range titles {
		r := 8
		if i < len(ratings) {
			r = ratings[i]
		}
		items = append(items, item{
			Rating: r,
			Show:   show{Title: t, Year: 2020 + i, IDs: ids{Trakt: i + 1}},
		})
	}
	b, _ := json.Marshal(items)
	return b
}

func searchResponse(title string, year int) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
		IMDB  string `json:"imdb"`
		TMDB  int    `json:"tmdb"`
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
	b, _ := json.Marshal([]item{{
		Type:  "show",
		Score: 1000,
		Show: show{
			Title:    title,
			Year:     year,
			IDs:      ids{Trakt: 1, Slug: "slug", IMDB: "tt1234567", TMDB: 42},
			Overview: "A great show",
			Rating:   8.5,
			Votes:    10000,
			Genres:   []string{"drama", "thriller"},
		},
	}})
	return b
}

func mockServer(t *testing.T, path string, body []byte, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path != "" && r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body) //nolint:errcheck
	}))
}

// --- tests ---

func TestGetListTrending(t *testing.T) {
	srv := mockServer(t, "/shows/trending", trendingResponse([]string{"Breaking Bad", "The Wire"}), 200)
	defer srv.Close()
	BaseURL = srv.URL

	c := New("test-key")
	items, err := c.GetList(context.Background(), "shows", "trending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].Title != "Breaking Bad" {
		t.Errorf("item[0]: got %q", items[0].Title)
	}
}

func TestGetListPopular(t *testing.T) {
	srv := mockServer(t, "/shows/popular", popularResponse([]string{"Severance", "Andor"}), 200)
	defer srv.Close()
	BaseURL = srv.URL

	c := New("test-key")
	items, err := c.GetList(context.Background(), "shows", "popular", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2, got %d", len(items))
	}
	if items[1].Title != "Andor" {
		t.Errorf("item[1]: got %q", items[1].Title)
	}
}

func TestGetListWatchlist(t *testing.T) {
	srv := mockServer(t, "/users/me/watchlist/shows", watchlistResponse([]string{"Dune: Prophecy"}), 200)
	defer srv.Close()
	BaseURL = srv.URL

	c := NewWithToken("key", "token123")
	items, err := c.GetList(context.Background(), "shows", "watchlist", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Dune: Prophecy" {
		t.Errorf("got %v", items)
	}
}

func TestGetListRatings(t *testing.T) {
	srv := mockServer(t, "/users/me/ratings/shows",
		ratingsResponse([]string{"Show A", "Show B"}, []int{9, 7}), 200)
	defer srv.Close()
	BaseURL = srv.URL

	c := NewWithToken("key", "token123")
	items, err := c.GetList(context.Background(), "shows", "ratings", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2, got %d", len(items))
	}
	if items[0].UserRating != 9 {
		t.Errorf("userRating: got %d", items[0].UserRating)
	}
	if items[1].UserRating != 7 {
		t.Errorf("userRating: got %d", items[1].UserRating)
	}
}

func historyResponse(titles []string) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
	}
	type movie struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	type item struct {
		Plays         int    `json:"plays"`
		LastWatchedAt string `json:"last_watched_at"`
		Movie         movie  `json:"movie"`
	}
	var items []item
	for i, t := range titles {
		items = append(items, item{
			Plays:         1,
			LastWatchedAt: "2024-01-01T00:00:00.000Z",
			Movie:         movie{Title: t, Year: 2020 + i, IDs: ids{Trakt: i + 1, Slug: "slug-" + t}},
		})
	}
	b, _ := json.Marshal(items)
	return b
}

func recommendationsResponse(titles []string) []byte {
	type ids struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
	}
	type movie struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	type item struct {
		Score float64 `json:"score"`
		Movie movie   `json:"movie"`
	}
	var items []item
	for i, t := range titles {
		items = append(items, item{
			Score: float64(100 - i),
			Movie: movie{Title: t, Year: 2020 + i, IDs: ids{Trakt: i + 1, Slug: "slug-" + t}},
		})
	}
	b, _ := json.Marshal(items)
	return b
}

func TestGetListHistory(t *testing.T) {
	srv := mockServer(t, "/users/me/watched/movies", historyResponse([]string{"Inception", "Interstellar"}), 200)
	defer srv.Close()
	BaseURL = srv.URL

	c := NewWithToken("key", "token123")
	items, err := c.GetList(context.Background(), "movies", "history", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].Title != "Inception" {
		t.Errorf("item[0]: got %q", items[0].Title)
	}
	if items[1].Title != "Interstellar" {
		t.Errorf("item[1]: got %q", items[1].Title)
	}
}

func TestGetListHistoryRequiresToken(t *testing.T) {
	BaseURL = "http://unused"
	c := New("key")
	_, err := c.GetList(context.Background(), "movies", "history", 0)
	if err == nil {
		t.Error("expected error when history requested without token")
	}
}

func TestGetListRecommendations(t *testing.T) {
	srv := mockServer(t, "/users/me/recommendations/movies", recommendationsResponse([]string{"Dune", "Arrival"}), 200)
	defer srv.Close()
	BaseURL = srv.URL

	c := NewWithToken("key", "token123")
	items, err := c.GetList(context.Background(), "movies", "recommendations", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].Title != "Dune" {
		t.Errorf("item[0]: got %q", items[0].Title)
	}
}

func TestGetListRequiresToken(t *testing.T) {
	BaseURL = "http://unused"
	c := New("key") // no token
	_, err := c.GetList(context.Background(), "shows", "watchlist", 0)
	if err == nil {
		t.Error("expected error when watchlist requested without token")
	}
}

func TestGetListUnknownList(t *testing.T) {
	BaseURL = "http://unused"
	c := New("key")
	_, err := c.GetList(context.Background(), "shows", "unknown", 0)
	if err == nil {
		t.Error("expected error for unknown list")
	}
}

func TestSearch(t *testing.T) {
	srv := mockServer(t, "/search/show", searchResponse("Breaking Bad", 2008), 200)
	defer srv.Close()
	BaseURL = srv.URL

	c := New("test-key")
	items, err := c.Search(context.Background(), "show", "Breaking Bad")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least 1 result")
	}
	it := items[0]
	if it.Title != "Breaking Bad" {
		t.Errorf("title: got %q", it.Title)
	}
	if it.Year != 2008 {
		t.Errorf("year: got %d", it.Year)
	}
	if it.IDs.IMDB != "tt1234567" {
		t.Errorf("imdb: got %q", it.IDs.IMDB)
	}
	if it.Rating != 8.5 {
		t.Errorf("rating: got %f", it.Rating)
	}
	if len(it.Genres) != 2 {
		t.Errorf("genres: got %v", it.Genres)
	}
}

func TestGetListWatchlistPaginates(t *testing.T) {
	// Simulate two pages of watchlist results.
	page1 := watchlistResponse([]string{"Show A", "Show B"})
	page2 := watchlistResponse([]string{"Show C"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Pagination-Page-Count", "2")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.Write(page2) //nolint:errcheck
		} else {
			w.Write(page1) //nolint:errcheck
		}
	}))
	defer srv.Close()
	BaseURL = srv.URL

	c := NewWithToken("key", "token123")
	items, err := c.GetList(context.Background(), "shows", "watchlist", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items across 2 pages, got %d", len(items))
	}
	titles := []string{items[0].Title, items[1].Title, items[2].Title}
	for i, want := range []string{"Show A", "Show B", "Show C"} {
		if titles[i] != want {
			t.Errorf("item[%d]: got %q, want %q", i, titles[i], want)
		}
	}
}

func TestGetListWatchlistPartialPaginationError(t *testing.T) {
	// Page 1 succeeds; page 2 returns a server error.
	// Expect partial results and a non-nil error.
	page1 := watchlistResponse([]string{"Show A", "Show B"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Pagination-Page-Count", "2")
		w.Header().Set("Content-Type", "application/json")
		w.Write(page1) //nolint:errcheck
	}))
	defer srv.Close()
	BaseURL = srv.URL

	c := NewWithToken("key", "token123")
	items, err := c.GetList(context.Background(), "shows", "watchlist", 0)
	if err == nil {
		t.Fatal("expected non-nil error on page 2 failure")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 partial items from page 1, got %d", len(items))
	}
	if items[0].Title != "Show A" || items[1].Title != "Show B" {
		t.Errorf("unexpected titles: %v", []string{items[0].Title, items[1].Title})
	}
}

func TestHTTPError(t *testing.T) {
	srv := mockServer(t, "", nil, 503)
	defer srv.Close()
	BaseURL = srv.URL

	c := New("key")
	_, err := c.GetList(context.Background(), "shows", "trending", 10)
	if err == nil {
		t.Error("expected error on HTTP 503")
	}
}
