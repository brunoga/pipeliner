package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

// --- mock server helpers ---

func trendingShows(titles []string) []byte {
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
		Watchers int  `json:"watchers"`
		Show     show `json:"show"`
	}
	var items []item
	for i, t := range titles {
		items = append(items, item{Watchers: 10, Show: show{Title: t, Year: 2020, IDs: ids{Trakt: i + 1}}})
	}
	b, _ := json.Marshal(items)
	return b
}


func mockServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
}

func tc() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func makeFilter(t *testing.T, cfg map[string]any) *traktFilter {
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
	return p.(*traktFilter)
}

// --- tests ---

func TestFilterAcceptsMatchingShow(t *testing.T) {
	srv := mockServer(t, trendingShows([]string{"Breaking Bad", "The Wire"}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makeFilter(t, map[string]any{
		"client_id": "key",
		"type":      "shows",
		"list":      "trending",
	})

	e := entry.New("Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1")
	if err := p.Filter(context.Background(), tc(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Error("want accepted, got not accepted")
	}
}

func TestFilterRejectsNonMatchByDefault(t *testing.T) {
	srv := mockServer(t, trendingShows([]string{"Breaking Bad"}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makeFilter(t, map[string]any{
		"client_id": "key",
		"type":      "shows",
		"list":      "trending",
	})

	e := entry.New("Some.Other.Show.S01E01.720p", "http://example.com/1")
	p.Filter(context.Background(), tc(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Errorf("non-matching show should be rejected by default")
	}
}

func TestFilterUnmatchedOptOut(t *testing.T) {
	srv := mockServer(t, trendingShows([]string{"Breaking Bad"}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makeFilter(t, map[string]any{
		"client_id":        "key",
		"type":             "shows",
		"list":             "trending",
		"reject_unmatched": false,
	})

	e := entry.New("Some.Other.Show.S01E01.720p", "http://example.com/1")
	p.Filter(context.Background(), tc(), e) //nolint:errcheck
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("want undecided when reject_unmatched=false, got accepted=%v rejected=%v", e.IsAccepted(), e.IsRejected())
	}
}

func TestFilterTitleCachedWithinTTL(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write(trendingShows([]string{"Breaking Bad"})) //nolint:errcheck
	}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makeFilter(t, map[string]any{
		"client_id": "key",
		"type":      "shows",
		"list":      "trending",
		"ttl":       "1h",
	})

	e1 := entry.New("Breaking.Bad.S01E01.720p", "http://example.com/1")
	e2 := entry.New("Breaking.Bad.S01E02.720p", "http://example.com/2")
	p.Filter(context.Background(), tc(), e1) //nolint:errcheck
	p.Filter(context.Background(), tc(), e2) //nolint:errcheck

	if callCount != 1 {
		t.Errorf("API called %d times within TTL, want 1", callCount)
	}
}

func TestFilterRefreshesAfterTTL(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write(trendingShows([]string{"Breaking Bad"})) //nolint:errcheck
	}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makeFilter(t, map[string]any{
		"client_id": "key",
		"type":      "shows",
		"list":      "trending",
		"ttl":       "1ms",
	})

	e1 := entry.New("Breaking.Bad.S01E01.720p", "http://example.com/1")
	p.Filter(context.Background(), tc(), e1) //nolint:errcheck
	time.Sleep(5 * time.Millisecond)

	e2 := entry.New("Breaking.Bad.S01E02.720p", "http://example.com/2")
	p.Filter(context.Background(), tc(), e2) //nolint:errcheck

	if callCount < 2 {
		t.Errorf("API called %d times, want ≥2 (once per TTL expiry)", callCount)
	}
}

func TestFilterWatchlistRequiresToken(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{
		"client_id": "key",
		"type":      "shows",
		"list":      "watchlist",
		// no access_token
	}, db)
	// Factory succeeds; error comes at fetch time (tested via GetList)
	if err != nil {
		t.Fatal(err) // factory should not fail
	}
}

func TestFilterMinRating(t *testing.T) {
	type ids struct{ Trakt int `json:"trakt"` }
	type show struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	type item struct {
		Rating int  `json:"rating"`
		Show   show `json:"show"`
	}
	body, _ := json.Marshal([]item{
		{Rating: 9, Show: show{Title: "High Rated Show", Year: 2020, IDs: ids{1}}},
		{Rating: 5, Show: show{Title: "Low Rated Show", Year: 2020, IDs: ids{2}}},
	})

	srv := mockServer(t, body)
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makeFilter(t, map[string]any{
		"client_id":    "key",
		"access_token": "tok",
		"type":         "shows",
		"list":         "ratings",
		"min_rating":   7,
	})

	highRated := entry.New("High.Rated.Show.S01E01.720p", "http://example.com/1")
	lowRated := entry.New("Low.Rated.Show.S01E01.720p", "http://example.com/2")

	p.Filter(context.Background(), tc(), highRated) //nolint:errcheck
	p.Filter(context.Background(), tc(), lowRated)  //nolint:errcheck

	if !highRated.IsAccepted() {
		t.Error("high-rated show should be accepted")
	}
	if lowRated.IsAccepted() {
		t.Error("low-rated show should not be accepted (below min_rating)")
	}
}

func TestFilterNonParseableTitle(t *testing.T) {
	srv := mockServer(t, trendingShows([]string{"Breaking Bad"}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makeFilter(t, map[string]any{
		"client_id": "key",
		"type":      "shows",
		"list":      "trending",
	})

	// A plain article URL with no series pattern — should not error or panic
	e := entry.New("Some Article Title", "http://example.com/article")
	if err := p.Filter(context.Background(), tc(), e); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if e.IsAccepted() {
		t.Error("non-parseable title should not be accepted")
	}
}

func TestInvalidType(t *testing.T) {
	_, err := newPlugin(map[string]any{"client_id": "k", "type": "podcasts"}, nil)
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestMissingClientID(t *testing.T) {
	_, err := newPlugin(map[string]any{"type": "shows"}, nil)
	if err == nil {
		t.Error("expected error for missing client_id")
	}
}

func TestInvalidTTL(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"client_id": "key",
		"type":      "shows",
		"ttl":       "not-a-duration",
	}, nil)
	if err == nil {
		t.Error("expected error for invalid ttl")
	}
}

func TestEmptyListNotCached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]")) //nolint:errcheck
	}))
	defer srv.Close()
	itrakt.BaseURL = srv.URL

	p := makeFilter(t, map[string]any{
		"client_id": "key",
		"type":      "shows",
		"list":      "trending",
		"ttl":       "1h",
	})

	e1 := entry.New("Breaking.Bad.S01E01.720p", "http://x.com/1")
	e2 := entry.New("Breaking.Bad.S01E02.720p", "http://x.com/2")
	p.Filter(context.Background(), tc(), e1) //nolint:errcheck
	p.Filter(context.Background(), tc(), e2) //nolint:errcheck

	if callCount < 2 {
		t.Errorf("empty list should not be cached; API called %d times, want ≥2", callCount)
	}
}

func TestPluginRegistered(t *testing.T) {
	if _, ok := plugin.Lookup("trakt"); !ok {
		t.Error("trakt plugin not registered")
	}
}
