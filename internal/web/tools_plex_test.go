package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/mediaserver"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/store"
)

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
	resp, err := http.Get(ts.URL + "/api/tools/plex")
	if err != nil {
		t.Fatal(err)
	}
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
	resp, _ = http.Get(ts.URL + "/api/tools/plex")
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
