package torrentclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// qbtCall records one non-login request to the mock qBittorrent server.
type qbtCall struct {
	Path string
	Form map[string]string
}

// newQbtServer builds a mock qBittorrent Web API. torrents/info returns the
// given payload; control endpoints in okPaths return 200, everything else 404.
func newQbtServer(t *testing.T, info []map[string]any, okPaths map[string]bool) (*httptest.Server, *[]qbtCall, *int) {
	t.Helper()
	var calls []qbtCall
	logins := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			logins++
			if r.FormValue("username") != "admin" || r.FormValue("password") != "secret" {
				w.Write([]byte("Fails.")) //nolint:errcheck
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "mock"})
			w.Write([]byte("Ok.")) //nolint:errcheck
		case "/api/v2/torrents/info":
			if err := json.NewEncoder(w).Encode(info); err != nil {
				t.Errorf("encode info: %v", err)
			}
		default:
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			form := map[string]string{}
			for k := range r.Form {
				form[k] = r.Form.Get(k)
			}
			calls = append(calls, qbtCall{Path: r.URL.Path, Form: form})
			if okPaths[r.URL.Path] {
				w.Write([]byte("")) //nolint:errcheck
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, &calls, &logins
}

func newQbtClient(t *testing.T, srvURL string) Client {
	t.Helper()
	host, port := hostPort(t, srvURL)
	c, err := New(BackendQBittorrent, Config{
		Host: host, Port: port, Username: "admin", Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestQBittorrentListTorrents(t *testing.T) {
	added := time.Now().Add(-48 * time.Hour).Unix()
	activity := time.Now().Add(-1 * time.Hour).Unix()
	info := []map[string]any{
		{
			"hash": "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
			"name": "Show.S01E01.1080p", "state": "uploading",
			"ratio": 1.8, "seeding_time": 7200,
			"added_on": added, "last_activity": activity,
			"progress": 1.0, "save_path": "/downloads",
		},
		{
			"hash": "1111111111111111111111111111111111111111",
			"name": "Missing.Files", "state": "missingFiles",
			"ratio": 0.0, "seeding_time": 0,
			"added_on": added, "last_activity": 0,
			"progress": 0.3, "save_path": "/downloads",
		},
		{
			"hash": "2222222222222222222222222222222222222222",
			"name": "Stalled.DL", "state": "stalledDL",
			"ratio": 0.0, "seeding_time": 0,
			"added_on": added, "last_activity": activity,
			"progress": 0.0, "save_path": "/downloads",
		},
		{
			"hash": "3333333333333333333333333333333333333333",
			"name": "Paused.New.Name", "state": "stoppedDL",
			"ratio": 0.0, "seeding_time": 0,
			"added_on": added, "last_activity": activity,
			"progress": 0.5, "save_path": "/downloads",
		},
		{
			"hash": "4444444444444444444444444444444444444444",
			"name": "Checking", "state": "checkingResumeData",
			"ratio": 0.0, "seeding_time": 0,
			"added_on": added, "last_activity": activity,
			"progress": 0.5, "save_path": "/downloads",
		},
		{
			"hash": "5555555555555555555555555555555555555555",
			"name": "Fetching.Metadata", "state": "metaDL",
			"ratio": 0.0, "seeding_time": 0,
			"added_on": added, "last_activity": activity,
			"progress": 0.0, "save_path": "/downloads",
		},
		{
			"hash": "6666666666666666666666666666666666666666",
			"name": "Seeding.No.Peers", "state": "stalledUP",
			"ratio": 3.2, "seeding_time": 900,
			"added_on": added, "last_activity": activity,
			"progress": 1.0, "save_path": "/downloads",
		},
	}
	srv, _, logins := newQbtServer(t, info, nil)
	defer srv.Close()

	c := newQbtClient(t, srv.URL)
	torrents, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(torrents) != 7 {
		t.Fatalf("got %d torrents, want 7", len(torrents))
	}
	if *logins != 1 {
		t.Errorf("logins = %d, want 1", *logins)
	}

	seeding := torrents[0]
	if seeding.Hash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("hash not lowercased: %q", seeding.Hash)
	}
	if seeding.State != StateSeeding {
		t.Errorf("uploading state = %q", seeding.State)
	}
	if seeding.Ratio != 1.8 {
		t.Errorf("ratio = %v", seeding.Ratio)
	}
	if seeding.SeedTime != 2*time.Hour {
		t.Errorf("seed time = %v", seeding.SeedTime)
	}
	if seeding.Progress != 100 {
		t.Errorf("progress = %v", seeding.Progress)
	}
	if seeding.DownloadDir != "/downloads" {
		t.Errorf("download dir = %q", seeding.DownloadDir)
	}
	if seeding.AddedAt.Unix() != added {
		t.Errorf("added at = %v", seeding.AddedAt)
	}

	errored := torrents[1]
	if errored.State != StateErrored {
		t.Errorf("missingFiles state = %q", errored.State)
	}
	if errored.Error != "qBittorrent state: missingFiles" {
		t.Errorf("error message = %q", errored.Error)
	}
	if !errored.LastActivity.IsZero() {
		t.Errorf("zero last_activity should map to zero time, got %v", errored.LastActivity)
	}
	if errored.Progress != 30 {
		t.Errorf("progress = %v, want 30", errored.Progress)
	}

	if torrents[2].State != StateStalled {
		t.Errorf("stalledDL state = %q", torrents[2].State)
	}
	if torrents[3].State != StatePaused {
		t.Errorf("stoppedDL state = %q", torrents[3].State)
	}
	if torrents[4].State != StateChecking {
		t.Errorf("checkingResumeData state = %q", torrents[4].State)
	}
	if torrents[5].State != StateDownloading {
		t.Errorf("metaDL state = %q", torrents[5].State)
	}
	if torrents[6].State != StateSeeding {
		t.Errorf("stalledUP state = %q", torrents[6].State)
	}
}

func TestQBittorrentLoginFailure(t *testing.T) {
	srv, _, _ := newQbtServer(t, nil, nil)
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	c, err := New(BackendQBittorrent, Config{
		Host: host, Port: port, Username: "admin", Password: "wrong",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTorrents(context.Background()); err == nil {
		t.Fatal("expected login failure error")
	}
}

func TestQBittorrentControlOps(t *testing.T) {
	ok := map[string]bool{
		"/api/v2/torrents/delete":     true,
		"/api/v2/torrents/pause":      true,
		"/api/v2/torrents/reannounce": true,
	}
	srv, calls, _ := newQbtServer(t, nil, ok)
	defer srv.Close()

	c := newQbtClient(t, srv.URL)
	ctx := context.Background()
	hashes := []string{"aaaa", "bbbb"}

	if err := c.Remove(ctx, hashes, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := c.Pause(ctx, hashes); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := c.Reannounce(ctx, hashes); err != nil {
		t.Fatalf("Reannounce: %v", err)
	}

	if len(*calls) != 3 {
		t.Fatalf("server saw %d control calls, want 3", len(*calls))
	}
	del := (*calls)[0]
	if del.Path != "/api/v2/torrents/delete" {
		t.Errorf("call 0 path = %q", del.Path)
	}
	if del.Form["hashes"] != "aaaa|bbbb" {
		t.Errorf("hashes = %q", del.Form["hashes"])
	}
	if del.Form["deleteFiles"] != "true" {
		t.Errorf("deleteFiles = %q", del.Form["deleteFiles"])
	}
	if (*calls)[1].Path != "/api/v2/torrents/pause" {
		t.Errorf("call 1 path = %q", (*calls)[1].Path)
	}
	if (*calls)[2].Path != "/api/v2/torrents/reannounce" {
		t.Errorf("call 2 path = %q", (*calls)[2].Path)
	}
}

// TestQBittorrentPauseFallsBackToStop covers qBittorrent 5.x, where
// torrents/pause was renamed to torrents/stop.
func TestQBittorrentPauseFallsBackToStop(t *testing.T) {
	ok := map[string]bool{"/api/v2/torrents/stop": true} // pause → 404
	srv, calls, _ := newQbtServer(t, nil, ok)
	defer srv.Close()

	c := newQbtClient(t, srv.URL)
	if err := c.Pause(context.Background(), []string{"aaaa"}); err != nil {
		t.Fatalf("Pause with stop fallback: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("server saw %d calls, want 2 (pause 404 then stop)", len(*calls))
	}
	if (*calls)[0].Path != "/api/v2/torrents/pause" || (*calls)[1].Path != "/api/v2/torrents/stop" {
		t.Errorf("call order = %q, %q", (*calls)[0].Path, (*calls)[1].Path)
	}
	if (*calls)[1].Form["hashes"] != "aaaa" {
		t.Errorf("stop hashes = %q", (*calls)[1].Form["hashes"])
	}
}

func TestQBittorrentRemoveHTTPError(t *testing.T) {
	srv, _, _ := newQbtServer(t, nil, nil) // all control endpoints 404
	defer srv.Close()

	c := newQbtClient(t, srv.URL)
	if err := c.Remove(context.Background(), []string{"aaaa"}, false); err == nil {
		t.Fatal("expected HTTP error")
	}
}
