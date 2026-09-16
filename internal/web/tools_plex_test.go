package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/mediaserver"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/store"
)

func getURL(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func newPlexToolServer(t *testing.T) (*httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tools/plex", srv.apiToolsPlexStatus)
	mux.HandleFunc("POST /api/tools/plex/reconcile", srv.apiToolsPlexReconcile)
	mux.HandleFunc("POST /api/tools/plex/forget", srv.apiToolsPlexForget)
	return httptest.NewServer(mux), db
}

func TestPlexToolStatusAndTokenPersistence(t *testing.T) {
	ts, db := newPlexToolServer(t)
	defer ts.Close()

	var status struct {
		HasToken bool `json:"has_token"`
	}
	resp := getURL(t, ts.URL+"/api/tools/plex")
	json.NewDecoder(resp.Body).Decode(&status) //nolint:errcheck
	resp.Body.Close()
	if status.HasToken {
		t.Error("no token should be saved initially")
	}

	// Reconcile without a token and none saved → 400.
	resp = postJSON(t, ts.URL+"/api/tools/plex/reconcile", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 without token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Simulate a saved token and confirm status flips.
	if err := db.Bucket(toolsSettingsBucket).Put(plexTokenKey, "tok"); err != nil {
		t.Fatal(err)
	}
	resp = getURL(t, ts.URL+"/api/tools/plex")
	json.NewDecoder(resp.Body).Decode(&status) //nolint:errcheck
	resp.Body.Close()
	if !status.HasToken {
		t.Error("has_token should be true after saving")
	}
}

// TestPlexToolReconcileEndToEnd runs the full flow against a fake plex.tv and
// a fake Plex server: two tracked movies, one in the library, one missing;
// then forgets the missing one.
func TestPlexToolReconcileEndToEnd(t *testing.T) {
	// Fake Plex server with one 3D section holding Avatar.
	plexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/identity":
			w.WriteHeader(http.StatusOK)
		case "/library/sections":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Directory": []map[string]any{{"key": "2", "type": "movie", "title": "3D Movies"}},
			}})
		case "/library/sections/2/all":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Metadata": []map[string]any{{"title": "Avatar", "year": 2009}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer plexSrv.Close()

	// Fake plex.tv resolving one owned server at the fake Plex URL.
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{ //nolint:errcheck
			"name": "Hex", "provides": "server", "owned": true, "accessToken": "srv-tok",
			"connections": []map[string]any{{"uri": plexSrv.URL, "local": false, "relay": false}},
		}})
	}))
	defer tv.Close()
	orig := mediaserver.PlexTVBaseURL
	mediaserver.PlexTVBaseURL = tv.URL
	defer func() { mediaserver.PlexTVBaseURL = orig }()

	ts, db := newPlexToolServer(t)
	defer ts.Close()

	// Tracker: Avatar 3D (in library) + Darkest Hour 3D (missing).
	tracker := imovies.NewTracker(db.Bucket(imovies.TrackerBucketName))
	if err := tracker.Mark(imovies.Record{Title: "avatar", Year: 2009, Is3D: true}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Mark(imovies.Record{Title: "the darkest hour", Year: 2011, Is3D: true}); err != nil {
		t.Fatal(err)
	}

	var result struct {
		Servers []struct {
			Name     string `json:"name"`
			Sections int    `json:"sections"`
		} `json:"servers"`
		Tracker int `json:"tracker"`
		Library int `json:"library"`
		Missing []struct {
			Key string `json:"key"`
		} `json:"missing"`
		Error string `json:"error"`
	}
	resp := postJSON(t, ts.URL+"/api/tools/plex/reconcile", map[string]any{"token": "acct"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reconcile: http %d", resp.StatusCode)
	}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	resp.Body.Close()

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if len(result.Servers) != 1 || result.Servers[0].Name != "Hex" || result.Servers[0].Sections != 1 {
		t.Errorf("servers: %+v", result.Servers)
	}
	if result.Tracker != 2 || result.Library != 1 {
		t.Errorf("counts: tracker=%d library=%d", result.Tracker, result.Library)
	}
	if len(result.Missing) != 1 || result.Missing[0].Key != "the darkest hour|2011|3d" {
		t.Fatalf("missing: %+v", result.Missing)
	}

	// The provided token is saved for next time.
	var tok string
	if found, _ := db.Bucket(toolsSettingsBucket).Get(plexTokenKey, &tok); !found || tok != "acct" {
		t.Errorf("token should be saved after successful discovery, got %q", tok)
	}

	// Forget the missing movie; it must vanish from the tracker.
	var forgot struct {
		Forgotten int `json:"forgotten"`
	}
	resp = postJSON(t, ts.URL+"/api/tools/plex/forget", map[string]any{"keys": []string{"the darkest hour|2011|3d"}})
	json.NewDecoder(resp.Body).Decode(&forgot) //nolint:errcheck
	resp.Body.Close()
	if forgot.Forgotten != 1 {
		t.Errorf("forgotten: %d", forgot.Forgotten)
	}
	if tracker.IsSeen("the darkest hour", 2011, true) {
		t.Error("forgotten movie should no longer be tracked")
	}
	if !tracker.IsSeen("avatar", 2009, true) {
		t.Error("untouched movie must stay tracked")
	}
}

// TestPlexSignInFlow drives the PIN flow against a fake plex.tv: start
// returns an approval URL; polling is pending until the fake attaches a
// token; the approved token is saved for the reconcile tool.
func TestPlexSignInFlow(t *testing.T) {
	approved := false
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins":
			if r.Header.Get("X-Plex-Client-Identifier") == "" {
				http.Error(w, "missing client identifier", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id": 777, "code": "abc123"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/pins/777":
			if approved {
				fmt.Fprint(w, `{"id": 777, "authToken": "linked-token"}`)
			} else {
				fmt.Fprint(w, `{"id": 777, "authToken": null}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer tv.Close()
	orig := mediaserver.PlexTVBaseURL
	mediaserver.PlexTVBaseURL = tv.URL
	defer func() { mediaserver.PlexTVBaseURL = orig }()

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/tools/plex/auth/start", srv.apiToolsPlexAuthStart)
	mux.HandleFunc("GET /api/tools/plex/auth/poll", srv.apiToolsPlexAuthPoll)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var pin struct {
		ID      int    `json:"id"`
		AuthURL string `json:"auth_url"`
	}
	resp := postJSON(t, ts.URL+"/api/tools/plex/auth/start", nil)
	json.NewDecoder(resp.Body).Decode(&pin) //nolint:errcheck
	resp.Body.Close()
	if pin.ID != 777 || !strings.Contains(pin.AuthURL, "app.plex.tv/auth") || !strings.Contains(pin.AuthURL, "abc123") {
		t.Fatalf("pin: %+v", pin)
	}

	var st struct {
		Done bool `json:"done"`
	}
	resp = getURL(t, ts.URL+"/api/tools/plex/auth/poll?id=777")
	json.NewDecoder(resp.Body).Decode(&st) //nolint:errcheck
	resp.Body.Close()
	if st.Done {
		t.Fatal("poll should be pending before approval")
	}

	approved = true
	resp = getURL(t, ts.URL+"/api/tools/plex/auth/poll?id=777")
	json.NewDecoder(resp.Body).Decode(&st) //nolint:errcheck
	resp.Body.Close()
	if !st.Done {
		t.Fatal("poll should report done after approval")
	}

	var tok string
	if found, _ := db.Bucket(toolsSettingsBucket).Get(plexTokenKey, &tok); !found || tok != "linked-token" {
		t.Errorf("approved token should be saved, got %q", tok)
	}
}
