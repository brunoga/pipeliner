package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchMovie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/search/movie" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("api_key") != "test-key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"results": []map[string]any{
				{"id": 27205, "title": "Inception", "release_date": "2010-07-16", "popularity": 99.5},
			},
			"page":          1,
			"total_results": 1,
		})
	}))
	defer srv.Close()

	c := New("test-key")
	c.BaseURL = srv.URL + "/3"

	results, err := c.SearchMovie(context.Background(), "Inception", 2010)
	if err != nil {
		t.Fatalf("SearchMovie: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Title != "Inception" {
		t.Errorf("title: got %q", results[0].Title)
	}
	if results[0].ID != 27205 {
		t.Errorf("id: got %d", results[0].ID)
	}
}

func TestGetMovie(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/27205" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id":                27205,
			"title":             "Inception",
			"release_date":      "2010-07-16",
			"runtime":           148,
			"tagline":           "Your mind is the scene of the crime.",
			"imdb_id":           "tt1375666",
			"original_language": "en",
			"genres":            []map[string]any{{"id": 28, "name": "Action"}},
			"production_countries": []map[string]any{
				{"iso_3166_1": "US", "name": "United States of America"},
			},
			"credits": map[string]any{
				"cast": []map[string]any{
					{"name": "Leonardo DiCaprio", "order": 0},
				},
			},
			"videos": map[string]any{
				"results": []map[string]any{
					{"key": "abc123", "site": "YouTube", "type": "Trailer"},
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
	}))
	defer srv.Close()

	c := New("test-key")
	c.BaseURL = srv.URL + "/3"

	m, err := c.GetMovie(context.Background(), 27205)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if m.Title != "Inception" {
		t.Errorf("title: got %q", m.Title)
	}
	if m.Runtime != 148 {
		t.Errorf("runtime: got %d", m.Runtime)
	}
	if m.ImdbID != "tt1375666" {
		t.Errorf("imdb_id: got %q", m.ImdbID)
	}
	if m.OriginalLanguage != "en" {
		t.Errorf("original_language: got %q", m.OriginalLanguage)
	}
	if len(m.ProductionCountries) == 0 || m.ProductionCountries[0].Name != "United States of America" {
		t.Errorf("production_countries: got %v", m.ProductionCountries)
	}
	if len(m.Credits.Cast) == 0 || m.Credits.Cast[0].Name != "Leonardo DiCaprio" {
		t.Errorf("credits.cast: got %v", m.Credits.Cast)
	}
	if len(m.Videos.Results) == 0 || m.Videos.Results[0].Key != "abc123" {
		t.Errorf("videos: got %v", m.Videos.Results)
	}
	if len(m.ReleaseDates.Results) == 0 || m.ReleaseDates.Results[0].ISO != "US" {
		t.Errorf("release_dates: got %v", m.ReleaseDates.Results)
	}
	if len(m.AlternativeTitles.Titles) == 0 || m.AlternativeTitles.Titles[0].Title != "Inception: The IMAX Experience" {
		t.Errorf("alternative_titles: got %v", m.AlternativeTitles.Titles)
	}
	for _, param := range []string{"credits", "videos", "release_dates", "alternative_titles"} {
		if !strings.Contains(gotQuery, param) {
			t.Errorf("expected %q in query string, got %q", param, gotQuery)
		}
	}
}

func TestSearchMovieHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New("test-key")
	c.BaseURL = srv.URL + "/3"

	_, err := c.SearchMovie(context.Background(), "anything", 0)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}
