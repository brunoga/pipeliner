package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/27205" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id":           27205,
			"title":        "Inception",
			"release_date": "2010-07-16",
			"runtime":      148,
			"tagline":      "Your mind is the scene of the crime.",
			"imdb_id":      "tt1375666",
			"genres":       []map[string]any{{"id": 28, "name": "Action"}},
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
