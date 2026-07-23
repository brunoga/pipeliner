package torrentclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type delugeRPCCall struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}

// mockDelugeDaemon records every RPC call and serves configurable responses.
type mockDelugeDaemon struct {
	calls    []delugeRPCCall
	loginOK  bool
	torrents map[string]any // core.get_torrents_status result
	rpcError string         // when set, every non-login call returns this RPC error
}

func (m *mockDelugeDaemon) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var call delugeRPCCall
		json.Unmarshal(body, &call) //nolint:errcheck
		m.calls = append(m.calls, call)

		w.Header().Set("Content-Type", "application/json")
		writeResult := func(result any) {
			json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": 1}) //nolint:errcheck
		}
		writeError := func(msg string) {
			json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"message": msg}, "id": 1}) //nolint:errcheck
		}

		if call.Method == "auth.login" {
			writeResult(m.loginOK)
			return
		}
		if m.rpcError != "" {
			writeError(m.rpcError)
			return
		}
		switch call.Method {
		case "core.get_torrents_status":
			writeResult(m.torrents)
		default:
			writeResult(nil)
		}
	}
}

func newTestDelugeClient(t *testing.T, mock *mockDelugeDaemon) *delugeClient {
	t.Helper()
	srv := httptest.NewServer(mock.handler())
	t.Cleanup(srv.Close)
	c := newDelugeClient(Config{Host: "127.0.0.1", Password: "secret"})
	c.endpoint = srv.URL + "/json"
	return c
}

func TestDelugeLoginSuccess(t *testing.T) {
	mock := &mockDelugeDaemon{loginOK: true, torrents: map[string]any{}}
	c := newTestDelugeClient(t, mock)

	if _, err := c.ListTorrents(context.Background()); err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(mock.calls) == 0 || mock.calls[0].Method != "auth.login" {
		t.Fatalf("first RPC should be auth.login, got %v", mock.calls)
	}
	if len(mock.calls[0].Params) != 1 || mock.calls[0].Params[0] != "secret" {
		t.Errorf("auth.login params: got %v, want [secret]", mock.calls[0].Params)
	}
}

func TestDelugeLoginFailure(t *testing.T) {
	mock := &mockDelugeDaemon{loginOK: false}
	c := newTestDelugeClient(t, mock)

	_, err := c.ListTorrents(context.Background())
	if err == nil {
		t.Fatal("expected error on failed login")
	}
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("error should mention login, got %q", err)
	}
	for _, call := range mock.calls {
		if call.Method == "core.get_torrents_status" {
			t.Error("no torrent listing should happen after a failed login")
		}
	}
}

func delugeTorrent(state, message string) map[string]any {
	return map[string]any{
		"name":              "t-" + state,
		"state":             state,
		"progress":          42.5,
		"ratio":             1.5,
		"time_added":        float64(1700000000),
		"seeding_time":      float64(3600),
		"download_location": "/downloads",
		"message":           message,
	}
}

func TestDelugeListTorrentsStateMapping(t *testing.T) {
	cases := []struct {
		delugeState string
		message     string
		want        State
		wantError   string
	}{
		{"Downloading", "OK", StateDownloading, ""},
		{"Seeding", "OK", StateSeeding, ""},
		{"Paused", "OK", StatePaused, ""},
		// Queued must NOT map to downloading: a zero-progress "downloading"
		// torrent is flagged by torrent_failed after stall_timeout, so queue
		// backlogs would be falsely janitored.
		{"Queued", "OK", StatePaused, ""},
		{"Checking", "OK", StateChecking, ""},
		{"Allocating", "OK", StateChecking, ""},
		{"Moving", "OK", StateChecking, ""},
		{"Error", "tracker unreachable", StateErrored, "tracker unreachable"},
		{"SomethingNew", "OK", StateDownloading, ""}, // unknown states fall back to downloading
	}

	torrents := map[string]any{}
	for i, tc := range cases {
		hash := strings.Repeat("a", 39) + string(rune('0'+i))
		torrents[hash] = delugeTorrent(tc.delugeState, tc.message)
	}
	mock := &mockDelugeDaemon{loginOK: true, torrents: torrents}
	c := newTestDelugeClient(t, mock)

	list, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(list) != len(cases) {
		t.Fatalf("torrent count: got %d, want %d", len(list), len(cases))
	}
	byName := make(map[string]Torrent, len(list))
	for _, tor := range list {
		byName[tor.Name] = tor
	}
	for _, tc := range cases {
		tor, ok := byName["t-"+tc.delugeState]
		if !ok {
			t.Errorf("%s: torrent missing from result", tc.delugeState)
			continue
		}
		if tor.State != tc.want {
			t.Errorf("%s: state got %q, want %q", tc.delugeState, tor.State, tc.want)
		}
		if tor.Error != tc.wantError {
			t.Errorf("%s: error got %q, want %q (healthy torrents must not carry Deluge's \"OK\" message)", tc.delugeState, tor.Error, tc.wantError)
		}
	}
}

func TestDelugeListTorrentsFieldExtraction(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef01234567"
	mock := &mockDelugeDaemon{loginOK: true, torrents: map[string]any{
		hash: delugeTorrent("Seeding", "OK"),
	}}
	c := newTestDelugeClient(t, mock)

	list, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("torrent count: got %d, want 1", len(list))
	}
	tor := list[0]
	if tor.Hash != hash {
		t.Errorf("hash: got %q", tor.Hash)
	}
	if tor.Name != "t-Seeding" {
		t.Errorf("name: got %q", tor.Name)
	}
	if tor.Progress != 42.5 {
		t.Errorf("progress: got %v, want 42.5", tor.Progress)
	}
	if tor.Ratio != 1.5 {
		t.Errorf("ratio: got %v, want 1.5", tor.Ratio)
	}
	if tor.SeedTime.Seconds() != 3600 {
		t.Errorf("seed time: got %v, want 1h", tor.SeedTime)
	}
	if tor.AddedAt.Unix() != 1700000000 {
		t.Errorf("added at: got %v", tor.AddedAt.Unix())
	}
	if tor.DownloadDir != "/downloads" {
		t.Errorf("download dir: got %q", tor.DownloadDir)
	}
}

func TestDelugeListTorrentsClampsNegativeRatio(t *testing.T) {
	torrent := delugeTorrent("Seeding", "OK")
	torrent["ratio"] = -1.0
	mock := &mockDelugeDaemon{loginOK: true, torrents: map[string]any{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": torrent,
	}}
	c := newTestDelugeClient(t, mock)

	list, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if list[0].Ratio != 0 {
		t.Errorf("negative ratio should clamp to 0, got %v", list[0].Ratio)
	}
}

func delugeCallsByMethod(mock *mockDelugeDaemon, method string) []delugeRPCCall {
	var out []delugeRPCCall
	for _, c := range mock.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func TestDelugeRemoveWithData(t *testing.T) {
	for _, withData := range []bool{true, false} {
		mock := &mockDelugeDaemon{loginOK: true}
		c := newTestDelugeClient(t, mock)

		hashes := []string{"hash-one", "hash-two"}
		if err := c.Remove(context.Background(), hashes, withData); err != nil {
			t.Fatalf("Remove(withData=%v): %v", withData, err)
		}
		calls := delugeCallsByMethod(mock, "core.remove_torrent")
		if len(calls) != 2 {
			t.Fatalf("withData=%v: got %d remove_torrent calls, want 2", withData, len(calls))
		}
		for i, call := range calls {
			if len(call.Params) != 2 {
				t.Fatalf("withData=%v: params %v", withData, call.Params)
			}
			if call.Params[0] != hashes[i] {
				t.Errorf("withData=%v: call %d hash got %v, want %q", withData, i, call.Params[0], hashes[i])
			}
			if got, _ := call.Params[1].(bool); got != withData {
				t.Errorf("withData=%v: call %d flag got %v", withData, i, call.Params[1])
			}
		}
	}
}

func TestDelugePause(t *testing.T) {
	mock := &mockDelugeDaemon{loginOK: true}
	c := newTestDelugeClient(t, mock)

	if err := c.Pause(context.Background(), []string{"h1", "h2"}); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	calls := delugeCallsByMethod(mock, "core.pause_torrent")
	if len(calls) != 1 {
		t.Fatalf("got %d pause_torrent calls, want 1", len(calls))
	}
	got, _ := calls[0].Params[0].([]any)
	if len(got) != 2 || got[0] != "h1" || got[1] != "h2" {
		t.Errorf("pause params: got %v, want [h1 h2]", calls[0].Params)
	}
}

func TestDelugePauseNoHashesIsNoOp(t *testing.T) {
	mock := &mockDelugeDaemon{loginOK: true}
	c := newTestDelugeClient(t, mock)

	if err := c.Pause(context.Background(), nil); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if calls := delugeCallsByMethod(mock, "core.pause_torrent"); len(calls) != 0 {
		t.Errorf("no pause_torrent RPC expected for empty hash list, got %d", len(calls))
	}
}

func TestDelugeReannounce(t *testing.T) {
	mock := &mockDelugeDaemon{loginOK: true}
	c := newTestDelugeClient(t, mock)

	if err := c.Reannounce(context.Background(), []string{"h1"}); err != nil {
		t.Fatalf("Reannounce: %v", err)
	}
	calls := delugeCallsByMethod(mock, "core.force_reannounce")
	if len(calls) != 1 {
		t.Fatalf("got %d force_reannounce calls, want 1", len(calls))
	}
	got, _ := calls[0].Params[0].([]any)
	if len(got) != 1 || got[0] != "h1" {
		t.Errorf("reannounce params: got %v, want [h1]", calls[0].Params)
	}
}

func TestDelugeRPCErrorPropagation(t *testing.T) {
	mock := &mockDelugeDaemon{loginOK: true, rpcError: "core failure"}
	c := newTestDelugeClient(t, mock)
	ctx := context.Background()

	if _, err := c.ListTorrents(ctx); err == nil || !strings.Contains(err.Error(), "core failure") {
		t.Errorf("ListTorrents should propagate the RPC error, got %v", err)
	}
	if err := c.Remove(ctx, []string{"h"}, false); err == nil || !strings.Contains(err.Error(), "core failure") {
		t.Errorf("Remove should propagate the RPC error, got %v", err)
	}
	if err := c.Pause(ctx, []string{"h"}); err == nil || !strings.Contains(err.Error(), "core failure") {
		t.Errorf("Pause should propagate the RPC error, got %v", err)
	}
	if err := c.Reannounce(ctx, []string{"h"}); err == nil || !strings.Contains(err.Error(), "core failure") {
		t.Errorf("Reannounce should propagate the RPC error, got %v", err)
	}
}

func TestDelugeNewClientDefaults(t *testing.T) {
	c := newDelugeClient(Config{Host: "example.org"})
	if c.endpoint != "http://example.org:8112/json" {
		t.Errorf("default endpoint: got %q", c.endpoint)
	}
	tlsClient := newDelugeClient(Config{Host: "example.org", Port: 9999, TLS: true})
	if tlsClient.endpoint != "https://example.org:9999/json" {
		t.Errorf("tls endpoint: got %q", tlsClient.endpoint)
	}
}
