package deluge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

type rpcCall struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}

// mockDeluge records all RPC calls and returns configurable responses.
type mockDeluge struct {
	calls    []rpcCall
	loginOK  bool
	addError string
}

func (m *mockDeluge) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var call rpcCall
		json.Unmarshal(body, &call) //nolint:errcheck
		m.calls = append(m.calls, call)

		w.Header().Set("Content-Type", "application/json")
		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": m.loginOK, "error": nil, "id": 1}) //nolint:errcheck
		case "core.add_torrent_url", "core.add_torrent_magnet":
			if m.addError != "" {
				json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"message": m.addError}, "id": 1}) //nolint:errcheck
			} else {
				json.NewEncoder(w).Encode(map[string]any{"result": "infohash123", "error": nil, "id": 1}) //nolint:errcheck
			}
		}
	}
}

func newTestPlugin(t *testing.T, srv *httptest.Server, extra map[string]any) *delugePlugin {
	t.Helper()
	p, err := newPlugin(map[string]any{
		"host":     "127.0.0.1",
		"port":     0, // overridden below
		"password": "secret",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dp := p.(*delugePlugin)
	dp.endpoint = srv.URL + "/json"
	for k, v := range extra {
		_ = k
		_ = v
	}
	return dp
}

func TestLoginAndAddTorrent(t *testing.T) {
	mock := &mockDeluge{loginOK: true}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	dp := newTestPlugin(t, srv, nil)
	e := entry.New("My Show S01E01", "http://example.com/ep.torrent")
	err := dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatal(err)
	}

	methods := make([]string, len(mock.calls))
	for i, c := range mock.calls {
		methods[i] = c.Method
	}
	if len(mock.calls) < 2 {
		t.Fatalf("expected at least 2 RPC calls, got %v", methods)
	}
	if mock.calls[0].Method != "auth.login" {
		t.Errorf("first call should be auth.login, got %q", mock.calls[0].Method)
	}
	if mock.calls[1].Method != "core.add_torrent_url" {
		t.Errorf("second call should be core.add_torrent_url, got %q", mock.calls[1].Method)
	}
	// Verify URL was passed.
	params := mock.calls[1].Params
	if len(params) < 1 || params[0] != "http://example.com/ep.torrent" {
		t.Errorf("torrent URL not passed correctly: %v", params)
	}
}

func TestLoginFailure(t *testing.T) {
	mock := &mockDeluge{loginOK: false}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	dp := newTestPlugin(t, srv, nil)
	e := entry.New("T", "http://x.com/a.torrent")
	err := dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e})
	if err == nil {
		t.Error("expected error on login failure")
	}
}

func TestAddTorrentError(t *testing.T) {
	mock := &mockDeluge{loginOK: true, addError: "some unexpected rpc error"}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	dp := newTestPlugin(t, srv, nil)
	e := entry.New("T", "http://x.com/a.torrent")
	err := dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Errorf("Output should not return error for per-entry add failure: %v", err)
	}
	if !e.IsFailed() {
		t.Error("entry should be Failed on unexpected Deluge error")
	}
}

func TestAlreadyInSessionConsumed(t *testing.T) {
	// When Deluge reports the torrent is already in session the entry must be
	// marked Consumed (not Failed) so that:
	//   - chained notification sinks (email, etc.) are skipped, and
	//   - CommitPlugin.Commit still runs so the seen plugin learns the URL.
	mock := &mockDeluge{loginOK: true, addError: "Torrent already in session (abc123)"}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	dp := newTestPlugin(t, srv, nil)
	e := entry.New("T", "http://x.com/a.torrent")
	e.Accept()
	err := dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatalf("deliver returned unexpected error: %v", err)
	}
	if !e.IsConsumed() {
		t.Error("entry should be Consumed when torrent is already in session")
	}
	if e.IsFailed() {
		t.Error("entry must not be Failed (Fail prevents CommitPlugin.Commit, causing the URL to never be learned)")
	}
	// Consumed entries must not appear in FilterAccepted so chained sinks are silenced.
	if len(entry.FilterAccepted([]*entry.Entry{e})) != 0 {
		t.Error("consumed entry should be excluded from FilterAccepted")
	}
}

func TestSavePathTemplate(t *testing.T) {
	mock := &mockDeluge{loginOK: true}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	p, _ := newPlugin(map[string]any{
		"host":     "127.0.0.1",
		"password": "x",
		"path":     "/downloads/{{.series_name}}",
	}, nil)
	dp := p.(*delugePlugin)
	dp.endpoint = srv.URL + "/json"

	e := entry.New("My Show S01E01", "http://x.com/a.torrent")
	e.Set("series_name", "My Show")
	dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	// Find the add_torrent_url call and check options.
	for _, c := range mock.calls {
		if c.Method == "core.add_torrent_url" && len(c.Params) >= 2 {
			opts, _ := c.Params[1].(map[string]any)
			if loc, _ := opts["download_location"].(string); !strings.Contains(loc, "My Show") {
				t.Errorf("download_location: got %q, want path containing 'My Show'", loc)
			}
		}
	}
}

func TestMagnetLinkUsesMagnetRPC(t *testing.T) {
	mock := &mockDeluge{loginOK: true}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	dp := newTestPlugin(t, srv, nil)
	magnet := "magnet:?xt=urn:btih:abc123&dn=My+Show+S01E01"
	e := entry.New("My Show S01E01", magnet)
	err := dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatal(err)
	}

	var addCall *rpcCall
	for i := range mock.calls {
		if mock.calls[i].Method == "core.add_torrent_magnet" || mock.calls[i].Method == "core.add_torrent_url" {
			addCall = &mock.calls[i]
			break
		}
	}
	if addCall == nil {
		t.Fatal("no add_torrent RPC call recorded")
	}
	if addCall.Method != "core.add_torrent_magnet" {
		t.Errorf("magnet link should use core.add_torrent_magnet, got %q", addCall.Method)
	}
	if len(addCall.Params) < 1 || addCall.Params[0] != magnet {
		t.Errorf("magnet URI not passed correctly: %v", addCall.Params)
	}
}

func TestTorrentLinkTypeMagnetUsesMagnetRPC(t *testing.T) {
	// torrent_link_type="magnet" must trigger core.add_torrent_magnet even
	// when the URL is not a magnet: URI (e.g. a Jackett proxy URL that was
	// resolved to a magnet by the Jackett parser but the URL was already set
	// to the actual magnet URI — this verifies the field is checked first).
	mock := &mockDeluge{loginOK: true}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	dp := newTestPlugin(t, srv, nil)
	magnet := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd"
	e := entry.New("movie", magnet)
	e.Set(entry.FieldTorrentLinkType, "magnet")
	dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	var addCall *rpcCall
	for i := range mock.calls {
		if mock.calls[i].Method == "core.add_torrent_magnet" || mock.calls[i].Method == "core.add_torrent_url" {
			addCall = &mock.calls[i]
			break
		}
	}
	if addCall == nil {
		t.Fatal("no add_torrent RPC call recorded")
	}
	if addCall.Method != "core.add_torrent_magnet" {
		t.Errorf("torrent_link_type=magnet should use core.add_torrent_magnet, got %q", addCall.Method)
	}
}

func TestTorrentLinkTypeTorrentUsesURLRPC(t *testing.T) {
	mock := &mockDeluge{loginOK: true}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	dp := newTestPlugin(t, srv, nil)
	e := entry.New("movie", "https://jackett.host/dl/idx/?key=abc&file=movie")
	e.Set(entry.FieldTorrentLinkType, "torrent")
	dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	for _, c := range mock.calls {
		if c.Method == "core.add_torrent_magnet" {
			t.Errorf("torrent_link_type=torrent should not use core.add_torrent_magnet")
		}
	}
}

func TestHTTPTorrentUsesURLRPC(t *testing.T) {
	mock := &mockDeluge{loginOK: true}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	dp := newTestPlugin(t, srv, nil)
	e := entry.New("My Show S01E01", "http://example.com/show.torrent")
	dp.deliver(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	for _, c := range mock.calls {
		if c.Method == "core.add_torrent_magnet" {
			t.Errorf("HTTP URL should not use core.add_torrent_magnet")
		}
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("deluge")
	if !ok {
		t.Fatal("deluge plugin not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("phase: got %v", d.Role)
	}
}
