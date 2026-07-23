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
