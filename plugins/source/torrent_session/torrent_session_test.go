package torrent_session

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "janitor",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func hostPort(t *testing.T, srvURL string) (string, int) {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname(), port
}

func TestGenerateTransmission(t *testing.T) {
	added := time.Now().Add(-24 * time.Hour).Unix()
	activity := time.Now().Add(-2 * time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const sid = "sid-1"
		if r.Header.Get("X-Transmission-Session-Id") != sid {
			w.Header().Set("X-Transmission-Session-Id", sid)
			w.WriteHeader(http.StatusConflict)
			return
		}
		resp := map[string]any{
			"result": "success",
			"arguments": map[string]any{
				"torrents": []map[string]any{
					{
						"hashString": "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
						"name":       "Show.S01E01.720p",
						"status":     6, "error": 0, "errorString": "",
						"isStalled": false, "percentDone": 1.0,
						"uploadRatio": 2.5, "secondsSeeding": 3600,
						"addedDate": added, "activityDate": activity,
						"downloadDir": "/data/tv",
					},
					{
						"hashString": "1111111111111111111111111111111111111111",
						"name":       "Dead.One",
						"status":     4, "error": 3, "errorString": "no data found",
						"isStalled": true, "percentDone": 0.0,
						"uploadRatio": -1.0, "secondsSeeding": 0,
						"addedDate": added, "activityDate": 0,
						"downloadDir": "/data/tv",
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	p, err := newPlugin(map[string]any{
		"backend": "transmission", "host": host, "port": port,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := p.(*sessionSourcePlugin).Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	e := entries[0]
	if e.Title != "Show.S01E01.720p" {
		t.Errorf("title = %q", e.Title)
	}
	if e.URL != "torrent://abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("url = %q", e.URL)
	}
	if got := e.GetString(entry.FieldTorrentInfoHash); got != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("hash = %q", got)
	}
	if got := e.GetString(entry.FieldTorrentState); got != "seeding" {
		t.Errorf("state = %q", got)
	}
	if v, _ := e.Get(entry.FieldTorrentRatio); v.(float64) != 2.5 {
		t.Errorf("ratio = %v", v)
	}
	if v, _ := e.Get(entry.FieldTorrentSeedTime); v.(int64) != 3600 {
		t.Errorf("seed_time = %v", v)
	}
	if v, _ := e.Get(entry.FieldTorrentProgress); v.(float64) != 100 {
		t.Errorf("progress = %v", v)
	}
	if got := e.GetString(entry.FieldTorrentDownloadDir); got != "/data/tv" {
		t.Errorf("download_dir = %q", got)
	}
	if e.GetTime(entry.FieldTorrentAddedAt).Unix() != added {
		t.Errorf("added_at = %v", e.GetTime(entry.FieldTorrentAddedAt))
	}
	if e.GetTime(entry.FieldTorrentLastActivity).Unix() != activity {
		t.Errorf("last_activity = %v", e.GetTime(entry.FieldTorrentLastActivity))
	}
	if got := e.GetString(entry.FieldSource); got != "torrent_session:transmission" {
		t.Errorf("source = %q", got)
	}
	if _, ok := e.Get(entry.FieldTorrentError); ok {
		t.Error("healthy torrent should not carry torrent_error")
	}

	dead := entries[1]
	if got := dead.GetString(entry.FieldTorrentState); got != "errored" {
		t.Errorf("dead state = %q", got)
	}
	if got := dead.GetString(entry.FieldTorrentError); got != "no data found" {
		t.Errorf("dead error = %q", got)
	}
	if _, ok := dead.Get(entry.FieldTorrentLastActivity); ok {
		t.Error("zero activity should leave torrent_last_activity unset")
	}
}

func TestGenerateQBittorrent(t *testing.T) {
	added := time.Now().Add(-6 * time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok.")) //nolint:errcheck
		case "/api/v2/torrents/info":
			info := []map[string]any{
				{
					"hash": "2222222222222222222222222222222222222222",
					"name": "Stalled.DL", "state": "stalledDL",
					"ratio": 0.0, "seeding_time": 0,
					"added_on": added, "last_activity": 0,
					"progress": 0.0, "save_path": "/downloads",
				},
			}
			json.NewEncoder(w).Encode(info) //nolint:errcheck
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	p, err := newPlugin(map[string]any{
		"backend": "qbittorrent", "host": host, "port": port,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := p.(*sessionSourcePlugin).Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.URL != "torrent://2222222222222222222222222222222222222222" {
		t.Errorf("url = %q", e.URL)
	}
	if got := e.GetString(entry.FieldTorrentState); got != "stalled" {
		t.Errorf("state = %q", got)
	}
	if got := e.GetString(entry.FieldSource); got != "torrent_session:qbittorrent" {
		t.Errorf("source = %q", got)
	}
}

func TestNewPluginRejectsUnknownBackend(t *testing.T) {
	if _, err := newPlugin(map[string]any{"backend": "deluge"}, nil); err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{"backend": "transmission"}); len(errs) != 0 {
		t.Errorf("valid config produced errors: %v", errs)
	}
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Error("missing backend should error")
	}
	if errs := validate(map[string]any{"backend": "deluge"}); len(errs) == 0 {
		t.Error("unsupported backend should error")
	}
	if errs := validate(map[string]any{"backend": "transmission", "bogus": 1}); len(errs) == 0 {
		t.Error("unknown key should error")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup(pluginName)
	if !ok {
		t.Fatal("torrent_session not registered")
	}
	if d.Role != plugin.RoleSource {
		t.Errorf("role = %v", d.Role)
	}
	for _, f := range []string{
		entry.FieldTorrentInfoHash, entry.FieldTorrentState, entry.FieldTorrentRatio,
		entry.FieldTorrentSeedTime, entry.FieldTorrentAddedAt, entry.FieldTorrentProgress,
		entry.FieldTorrentDownloadDir,
	} {
		found := false
		for _, p := range d.Produces {
			if p == f {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Produces missing %s", f)
		}
	}
}
