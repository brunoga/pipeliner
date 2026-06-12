package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakePluginLogCtl is a minimal in-memory PluginLogControl for testing the
// API surface in isolation from clog.
type fakePluginLogCtl struct {
	mu      sync.Mutex
	plugins []string
}

func (f *fakePluginLogCtl) DebugPlugins() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.plugins) == 0 {
		return nil
	}
	out := make([]string, len(f.plugins))
	copy(out, f.plugins)
	return out
}

func (f *fakePluginLogCtl) SetDebugPlugins(names []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plugins = append(f.plugins[:0], names...)
}

func newLogSettingsServer(t *testing.T, ctl PluginLogControl) (*httptest.Server, *Server) {
	t.Helper()
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	if ctl != nil {
		srv.SetPluginLogControl(ctl)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/log-debug-plugins", srv.apiGetLogDebugPlugins)
	mux.HandleFunc("PUT /api/log-debug-plugins", srv.apiPutLogDebugPlugins)
	return httptest.NewServer(mux), srv
}

// TestAPILogDebugPluginsGetEmpty confirms the GET endpoint returns plugins:[]
// (not null) when no overrides are registered, so the UI can treat the field
// as a stable array.
func TestAPILogDebugPluginsGetEmpty(t *testing.T) {
	ctl := &fakePluginLogCtl{}
	ts, _ := newLogSettingsServer(t, ctl)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/log-debug-plugins")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var body logDebugPluginsResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Plugins == nil {
		t.Errorf("plugins must be [] not null, got %v", body.Plugins)
	}
	if len(body.Plugins) != 0 {
		t.Errorf("plugins: got %v, want empty", body.Plugins)
	}
}

// TestAPILogDebugPluginsPutReplaces confirms PUT writes through to the control
// and that GET returns the new set in canonical (sorted, deduped) order.
func TestAPILogDebugPluginsPutReplaces(t *testing.T) {
	ctl := &fakePluginLogCtl{}
	ts, _ := newLogSettingsServer(t, ctl)
	defer ts.Close()

	body := `{"plugins":["metainfo_tvdb", "metainfo_bluray", "", "metainfo_bluray"]}`
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, ts.URL+"/api/log-debug-plugins",
		bytes.NewBufferString(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var put logDebugPluginsResp
	if err := json.NewDecoder(resp.Body).Decode(&put); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	want := []string{"metainfo_bluray", "metainfo_tvdb"}
	if !equalStrings(put.Plugins, want) {
		t.Errorf("PUT response: got %v, want %v", put.Plugins, want)
	}

	// The control was actually updated.
	if got := ctl.DebugPlugins(); !equalStrings(got, want) {
		t.Errorf("control state: got %v, want %v", got, want)
	}

	// And GET reflects the new state.
	r2 := get(t, ts.URL+"/api/log-debug-plugins")
	defer r2.Body.Close()
	var after logDebugPluginsResp
	_ = json.NewDecoder(r2.Body).Decode(&after)
	if !equalStrings(after.Plugins, want) {
		t.Errorf("GET after PUT: got %v, want %v", after.Plugins, want)
	}
}

// TestAPILogDebugPluginsPutClear confirms an empty plugins array clears the
// override — the most common round-trip for the user (toggle off via the UI).
func TestAPILogDebugPluginsPutClear(t *testing.T) {
	ctl := &fakePluginLogCtl{plugins: []string{"metainfo_bluray"}}
	ts, _ := newLogSettingsServer(t, ctl)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, ts.URL+"/api/log-debug-plugins",
		strings.NewReader(`{"plugins":[]}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if ctl.DebugPlugins() != nil {
		t.Errorf("control: expected nil after clear, got %v", ctl.DebugPlugins())
	}
}

// TestAPILogDebugPluginsPutBadJSON confirms malformed input gets 400, not 500.
func TestAPILogDebugPluginsPutBadJSON(t *testing.T) {
	ctl := &fakePluginLogCtl{}
	ts, _ := newLogSettingsServer(t, ctl)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, ts.URL+"/api/log-debug-plugins",
		strings.NewReader(`{nope`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

// TestAPILogDebugPluginsNotWired confirms both endpoints 501 when the daemon
// did not call SetPluginLogControl (e.g. CLI run mode, or unit-test harness).
func TestAPILogDebugPluginsNotWired(t *testing.T) {
	ts, _ := newLogSettingsServer(t, nil)
	defer ts.Close()

	r := get(t, ts.URL+"/api/log-debug-plugins")
	r.Body.Close()
	if r.StatusCode != http.StatusNotImplemented {
		t.Errorf("GET status: got %d, want 501", r.StatusCode)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, ts.URL+"/api/log-debug-plugins",
		strings.NewReader(`{"plugins":["x"]}`))
	r2, _ := http.DefaultClient.Do(req)
	r2.Body.Close()
	if r2.StatusCode != http.StatusNotImplemented {
		t.Errorf("PUT status: got %d, want 501", r2.StatusCode)
	}
}

