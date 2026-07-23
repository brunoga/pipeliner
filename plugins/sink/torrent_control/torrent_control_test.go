package torrent_control

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx(dryRun bool) *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "janitor",
		DryRun: dryRun,
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

// mockTransmission counts RPC calls and records the last method+arguments.
func mockTransmission(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const sid = "sid-1"
		if r.Header.Get("X-Transmission-Session-Id") != sid {
			w.Header().Set("X-Transmission-Session-Id", sid)
			w.WriteHeader(http.StatusConflict)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		calls = append(calls, req)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"result": "success", "arguments": map[string]any{},
		})
	}))
	return srv, &calls
}

func sessionEntry(title, hash string) *entry.Entry {
	e := entry.New(title, "torrent://"+strings.ToLower(hash))
	e.Set(entry.FieldTorrentInfoHash, strings.ToLower(hash))
	e.Accept("torrent_failed: test")
	return e
}

func newSink(t *testing.T, srvURL, action string) *controlSink {
	t.Helper()
	host, port := hostPort(t, srvURL)
	p, err := newPlugin(map[string]any{
		"action": action, "backend": "transmission", "host": host, "port": port,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*controlSink)
}

func TestDryRunMakesZeroClientCalls(t *testing.T) {
	srv, calls := mockTransmission(t)
	defer srv.Close()

	p := newSink(t, srv.URL, "remove_with_data")
	e := sessionEntry("Dead.Torrent", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	if err := p.Consume(context.Background(), makeCtx(true), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("dry-run made %d client calls, want 0", len(*calls))
	}
	if !e.IsAccepted() {
		t.Error("dry-run entry should be accepted with a preview reason")
	}
	if !strings.Contains(e.AcceptReason, "would remove (with data) Dead.Torrent") {
		t.Errorf("accept reason = %q", e.AcceptReason)
	}
}

func TestRemoveWithData(t *testing.T) {
	srv, calls := mockTransmission(t)
	defer srv.Close()

	p := newSink(t, srv.URL, "remove_with_data")
	e1 := sessionEntry("A", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	e2 := sessionEntry("B", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e1, e2}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("got %d rpc calls, want 1 batched call", len(*calls))
	}
	call := (*calls)[0]
	if call["method"] != "torrent-remove" {
		t.Errorf("method = %v", call["method"])
	}
	args := call["arguments"].(map[string]any)
	if del, _ := args["delete-local-data"].(bool); !del {
		t.Error("delete-local-data should be true for remove_with_data")
	}
	if ids := args["ids"].([]any); len(ids) != 2 {
		t.Errorf("ids = %v", ids)
	}
	if !e1.IsAccepted() || !e2.IsAccepted() {
		t.Error("entries should be accepted after successful removal")
	}
}

func TestRemoveWithoutData(t *testing.T) {
	srv, calls := mockTransmission(t)
	defer srv.Close()

	p := newSink(t, srv.URL, "remove")
	e := sessionEntry("A", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	args := (*calls)[0]["arguments"].(map[string]any)
	if del, _ := args["delete-local-data"].(bool); del {
		t.Error("delete-local-data should be false for remove")
	}
}

func TestPauseAndReannounce(t *testing.T) {
	for action, method := range map[string]string{
		"pause":      "torrent-stop",
		"reannounce": "torrent-reannounce",
	} {
		srv, calls := mockTransmission(t)
		p := newSink(t, srv.URL, action)
		e := sessionEntry("A", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
			t.Fatal(err)
		}
		if (*calls)[0]["method"] != method {
			t.Errorf("action %s: method = %v, want %s", action, (*calls)[0]["method"], method)
		}
		srv.Close()
	}
}

func TestMissingHashFails(t *testing.T) {
	srv, calls := mockTransmission(t)
	defer srv.Close()

	p := newSink(t, srv.URL, "remove")
	e := entry.New("No.Hash", "torrent://")
	e.Accept()

	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsFailed() {
		t.Error("entry without torrent_info_hash should be failed")
	}
	if len(*calls) != 0 {
		t.Errorf("made %d client calls, want 0", len(*calls))
	}
}

func TestClientErrorFailsEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newSink(t, srv.URL, "remove")
	e := sessionEntry("A", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err == nil {
		t.Fatal("expected error from failing client")
	}
	if !e.IsFailed() {
		t.Error("entry should be failed when the client call fails")
	}
}

func TestNewPluginValidation(t *testing.T) {
	if _, err := newPlugin(map[string]any{"action": "explode", "backend": "transmission"}, nil); err == nil {
		t.Error("invalid action should error")
	}
	if _, err := newPlugin(map[string]any{"action": "remove", "backend": "deluge"}, nil); err == nil {
		t.Error("unsupported backend should error")
	}
}

func TestValidate(t *testing.T) {
	ok := map[string]any{"action": "remove", "backend": "qbittorrent"}
	if errs := validate(ok); len(errs) != 0 {
		t.Errorf("valid config produced errors: %v", errs)
	}
	if errs := validate(map[string]any{"backend": "transmission"}); len(errs) == 0 {
		t.Error("missing action should error")
	}
	if errs := validate(map[string]any{"action": "remove"}); len(errs) == 0 {
		t.Error("missing backend should error")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup(pluginName)
	if !ok {
		t.Fatal("torrent_control not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("role = %v", d.Role)
	}
	if len(d.Requires) != 1 || d.Requires[0][0] != entry.FieldTorrentInfoHash {
		t.Errorf("Requires = %v", d.Requires)
	}
}
