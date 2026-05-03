package tvdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginAndSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data":   map[string]string{"token": "test-jwt"},
				"status": "success",
			})
		case "/v4/search":
			if r.URL.Query().Get("query") != "Breaking+Bad" && r.URL.Query().Get("query") != "Breaking Bad" {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": []map[string]any{
					{"tvdb_id": 81189, "name": "Breaking Bad", "year": "2008"},
				},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New("test-key")
	c.BaseURL = srv.URL + "/v4"

	results, err := c.SearchSeries(context.Background(), "Breaking Bad")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Name != "Breaking Bad" {
		t.Errorf("name: got %q", results[0].Name)
	}
	if results[0].ID != 81189 {
		t.Errorf("id: got %d", results[0].ID)
	}
}

func TestGetEpisodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data":   map[string]string{"token": "test-jwt"},
				"status": "success",
			})
		case "/v4/series/81189/episodes/official":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": map[string]any{
					"episodes": []map[string]any{
						{"id": 1, "seasonNumber": 1, "number": 1, "name": "Pilot", "aired": "2008-01-20"},
					},
				},
				"status": "success",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New("test-key")
	c.BaseURL = srv.URL + "/v4"

	eps, err := c.GetEpisodes(context.Background(), 81189)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(eps) == 0 {
		t.Fatal("expected at least one episode")
	}
	if eps[0].Name != "Pilot" {
		t.Errorf("episode name: got %q", eps[0].Name)
	}
}

func TestLoginError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New("bad-key")
	c.BaseURL = srv.URL + "/v4"

	_, err := c.SearchSeries(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
}
