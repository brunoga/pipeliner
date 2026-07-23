package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureServer records the last request and replies with a minimal
// SyncResponse.
func captureServer(t *testing.T, status int, response string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	var gotReq http.Request
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = *r
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = buf
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotReq, &gotBody
}

func TestAddListItemsWatchlist(t *testing.T) {
	srv, gotReq, gotBody := captureServer(t, http.StatusCreated,
		`{"added":{"shows":1},"existing":{"shows":0},"not_found":{"shows":[]}}`)
	BaseURL = srv.URL

	c := NewWithToken("cid", "tok")
	resp, err := c.AddListItems(context.Background(), "watchlist", ListItemsBody{
		Shows: []ListItem{{IDs: ItemIDs{Trakt: 42}}},
	})
	if err != nil {
		t.Fatalf("AddListItems: %v", err)
	}

	if gotReq.Method != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotReq.Method)
	}
	if gotReq.URL.Path != "/sync/watchlist" {
		t.Errorf("path: got %q, want /sync/watchlist", gotReq.URL.Path)
	}
	if h := gotReq.Header.Get("Authorization"); h != "Bearer tok" {
		t.Errorf("auth header: got %q", h)
	}
	if h := gotReq.Header.Get("trakt-api-key"); h != "cid" {
		t.Errorf("api key header: got %q", h)
	}
	if h := gotReq.Header.Get("trakt-api-version"); h != "2" {
		t.Errorf("api version header: got %q", h)
	}

	var body ListItemsBody
	if err := json.Unmarshal(*gotBody, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Shows) != 1 || body.Shows[0].IDs.Trakt != 42 {
		t.Errorf("body: got %+v", body)
	}
	if len(body.Movies) != 0 {
		t.Errorf("movies should be omitted, got %+v", body.Movies)
	}
	if resp.Added["shows"] != 1 {
		t.Errorf("added: got %v", resp.Added)
	}
}

func TestRemoveListItemsWatchlist(t *testing.T) {
	srv, gotReq, _ := captureServer(t, http.StatusOK,
		`{"deleted":{"shows":1},"not_found":{"shows":[]}}`)
	BaseURL = srv.URL

	c := NewWithToken("cid", "tok")
	resp, err := c.RemoveListItems(context.Background(), "watchlist", ListItemsBody{
		Shows: []ListItem{{IDs: ItemIDs{IMDB: "tt123"}}},
	})
	if err != nil {
		t.Fatalf("RemoveListItems: %v", err)
	}
	if gotReq.URL.Path != "/sync/watchlist/remove" {
		t.Errorf("path: got %q, want /sync/watchlist/remove", gotReq.URL.Path)
	}
	if resp.Deleted["shows"] != 1 {
		t.Errorf("deleted: got %v", resp.Deleted)
	}
}

func TestAddListItemsPersonalList(t *testing.T) {
	srv, gotReq, gotBody := captureServer(t, http.StatusCreated,
		`{"added":{"movies":1},"not_found":{"movies":[]}}`)
	BaseURL = srv.URL

	c := NewWithToken("cid", "tok")
	_, err := c.AddListItems(context.Background(), "my-list", ListItemsBody{
		Movies: []ListItem{{IDs: ItemIDs{TMDB: 7, IMDB: "tt9"}}},
	})
	if err != nil {
		t.Fatalf("AddListItems: %v", err)
	}
	if gotReq.URL.Path != "/users/me/lists/my-list/items" {
		t.Errorf("path: got %q, want /users/me/lists/my-list/items", gotReq.URL.Path)
	}

	var body ListItemsBody
	if err := json.Unmarshal(*gotBody, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Movies) != 1 || body.Movies[0].IDs.TMDB != 7 || body.Movies[0].IDs.IMDB != "tt9" {
		t.Errorf("body: got %+v", body)
	}
}

func TestRemoveListItemsPersonalList(t *testing.T) {
	srv, gotReq, _ := captureServer(t, http.StatusOK,
		`{"deleted":{"movies":1},"not_found":{"movies":[]}}`)
	BaseURL = srv.URL

	c := NewWithToken("cid", "tok")
	_, err := c.RemoveListItems(context.Background(), "my-list", ListItemsBody{
		Movies: []ListItem{{IDs: ItemIDs{Trakt: 1}}},
	})
	if err != nil {
		t.Fatalf("RemoveListItems: %v", err)
	}
	if gotReq.URL.Path != "/users/me/lists/my-list/items/remove" {
		t.Errorf("path: got %q, want /users/me/lists/my-list/items/remove", gotReq.URL.Path)
	}
}

func TestMutateListRequiresToken(t *testing.T) {
	c := New("cid") // no token
	_, err := c.AddListItems(context.Background(), "watchlist", ListItemsBody{
		Shows: []ListItem{{IDs: ItemIDs{Trakt: 1}}},
	})
	if err == nil {
		t.Fatal("expected error without access token")
	}
}

func TestMutateListRejectsEmptyBody(t *testing.T) {
	c := NewWithToken("cid", "tok")
	_, err := c.AddListItems(context.Background(), "watchlist", ListItemsBody{})
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestMutateListHTTPError(t *testing.T) {
	srv, _, _ := captureServer(t, http.StatusNotFound, `{}`)
	BaseURL = srv.URL

	c := NewWithToken("cid", "tok")
	_, err := c.AddListItems(context.Background(), "no-such-list", ListItemsBody{
		Shows: []ListItem{{IDs: ItemIDs{Trakt: 1}}},
	})
	if err == nil {
		t.Fatal("expected error on HTTP 404")
	}
}

func TestSyncResponseNotFoundDecodes(t *testing.T) {
	srv, _, _ := captureServer(t, http.StatusCreated,
		`{"added":{"shows":0},"not_found":{"shows":[{"ids":{"tvdb":999}}],"movies":[{"ids":{"imdb":"tt0"}}]}}`)
	BaseURL = srv.URL

	c := NewWithToken("cid", "tok")
	resp, err := c.AddListItems(context.Background(), "watchlist", ListItemsBody{
		Shows: []ListItem{{IDs: ItemIDs{TVDB: 999}}},
	})
	if err != nil {
		t.Fatalf("AddListItems: %v", err)
	}
	if len(resp.NotFound.Shows) != 1 || resp.NotFound.Shows[0].IDs.TVDB != 999 {
		t.Errorf("not_found shows: got %+v", resp.NotFound.Shows)
	}
	if len(resp.NotFound.Movies) != 1 || resp.NotFound.Movies[0].IDs.IMDB != "tt0" {
		t.Errorf("not_found movies: got %+v", resp.NotFound.Movies)
	}
}
