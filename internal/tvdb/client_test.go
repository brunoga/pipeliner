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
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]string{"token": "test-jwt"},
				"status": "success",
			})
		case "/v4/search":
			if r.URL.Query().Get("query") != "Breaking+Bad" && r.URL.Query().Get("query") != "Breaking Bad" {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"tvdb_id": "81189", "name": "Breaking Bad", "year": "2008"},
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
	if results[0].ID != "81189" {
		t.Errorf("id: got %q", results[0].ID)
	}
}

func TestGetEpisodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]string{"token": "test-jwt"},
				"status": "success",
			})
		case "/v4/series/81189/episodes/official":
			json.NewEncoder(w).Encode(map[string]any{
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

	eps, err := c.GetEpisodes(context.Background(), "81189")
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

func TestFlexStatusDecodesBothShapes(t *testing.T) {
	// Search results carry status as a plain string.
	var fromSearch Series
	if err := json.Unmarshal([]byte(`{"name":"X","status":"Continuing"}`), &fromSearch); err != nil {
		t.Fatalf("string-shaped status: %v", err)
	}
	if fromSearch.Status != "Continuing" {
		t.Errorf("string shape: got %q, want Continuing", fromSearch.Status)
	}

	// Base series records carry status as an object.
	var fromRecord Series
	if err := json.Unmarshal([]byte(`{"name":"X","status":{"id":2,"name":"Ended","recordType":"series"}}`), &fromRecord); err != nil {
		t.Fatalf("object-shaped status: %v", err)
	}
	if fromRecord.Status != "Ended" {
		t.Errorf("object shape: got %q, want Ended", fromRecord.Status)
	}

	// Absent status stays empty.
	var absent Series
	if err := json.Unmarshal([]byte(`{"name":"X"}`), &absent); err != nil {
		t.Fatalf("absent status: %v", err)
	}
	if absent.Status != "" {
		t.Errorf("absent status: got %q, want empty", absent.Status)
	}
}

func TestGetSeriesByIDDecodesObjectStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]string{"token": "test-jwt"},
				"status": "success",
			})
		case "/v4/series/81189":
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"name":   "Breaking Bad",
					"slug":   "breaking-bad",
					"status": map[string]any{"id": 2, "name": "Ended", "recordType": "series"},
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

	s, err := c.GetSeriesByID(context.Background(), 81189)
	if err != nil {
		t.Fatalf("GetSeriesByID: %v", err)
	}
	if s.Status != "Ended" {
		t.Errorf("status: got %q, want Ended", s.Status)
	}
}

func TestAddFavorite(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody map[string]int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]string{"token": "test-jwt"},
				"status": "success",
			})
		case "/v4/user/favorites":
			gotMethod = r.Method
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			gotContentType = r.Header.Get("Content-Type")
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated) // empty body, like the real API
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewWithPin("test-key", "test-pin")
	c.BaseURL = srv.URL + "/v4"

	if err := c.AddFavorite(context.Background(), 81189); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != "/v4/user/favorites" {
		t.Errorf("path: got %q", gotPath)
	}
	if gotAuth != "Bearer test-jwt" {
		t.Errorf("auth header: got %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("content type: got %q", gotContentType)
	}
	if gotBody["series"] != 81189 {
		t.Errorf("body: got %v, want {\"series\":81189}", gotBody)
	}
}

func TestAddFavoriteRequiresPin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/login" {
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]string{"token": "test-jwt"},
				"status": "success",
			})
			return
		}
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New("test-key") // no pin
	c.BaseURL = srv.URL + "/v4"

	if err := c.AddFavorite(context.Background(), 1); err == nil {
		t.Fatal("expected error without user_pin")
	}
}

func TestAddFavoriteHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/login" {
			json.NewEncoder(w).Encode(map[string]any{
				"data":   map[string]string{"token": "test-jwt"},
				"status": "success",
			})
			return
		}
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewWithPin("test-key", "test-pin")
	c.BaseURL = srv.URL + "/v4"

	if err := c.AddFavorite(context.Background(), 1); err == nil {
		t.Fatal("expected error on HTTP 401")
	}
}
