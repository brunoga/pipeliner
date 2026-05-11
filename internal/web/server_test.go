package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// Register a handful of plugins so plugin.All() is non-empty in tests.
	_ "github.com/brunoga/pipeliner/plugins/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/input/rss"
	_ "github.com/brunoga/pipeliner/plugins/modify/pathfmt"
)

// stubDaemon satisfies DaemonControl with no-op implementations.
type stubDaemon struct{}

func (s stubDaemon) NextRun(_ string) time.Time { return time.Time{} }
func (s stubDaemon) Trigger(_ string)           {}

// newTestServer builds a Server wired for testing.
// configPath and validateFn are optional; pass "" / nil to skip.
func newTestServer(t *testing.T, configPath string, validateFn func([]byte) []string) (*Server, *httptest.Server) {
	t.Helper()
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	if configPath != "" {
		srv.SetConfigPath(configPath)
	}
	if validateFn != nil {
		srv.SetConfigValidator(validateFn)
	}

	// Register all protected routes on a plain mux (no session middleware for tests).
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", srv.apiGetConfig)
	mux.HandleFunc("POST /api/config", srv.apiSaveConfig)
	mux.HandleFunc("POST /api/reload", srv.apiReload)

	return srv, httptest.NewServer(mux)
}

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// get and post are context-aware wrappers used in tests to satisfy noctx.
func get(t *testing.T, url string) *http.Response {
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

func post(t *testing.T, url, contentType string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ── GET /api/config ───────────────────────────────────────────────────────────

func TestGetConfigReturnsContent(t *testing.T) {
	dir := t.TempDir()
	content := "tasks:\n  test:\n    rss:\n      url: http://example.com\n"
	path := writeConfig(t, dir, content)

	_, ts := newTestServer(t, path, nil)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/config")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["content"] != content {
		t.Errorf("content mismatch:\ngot:  %q\nwant: %q", body["content"], content)
	}
}

func TestGetConfigNotConfigured(t *testing.T) {
	_, ts := newTestServer(t, "", nil)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/config")
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status: got %d, want 501", resp.StatusCode)
	}
}

// ── POST /api/config — dry_run ────────────────────────────────────────────────

func TestSaveConfigDryRunValid(t *testing.T) {
	dir := t.TempDir()
	original := "original content\n"
	path := writeConfig(t, dir, original)

	_, ts := newTestServer(t, path, func(_ []byte) []string { return nil })
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"content": "new content\n", "dry_run": true})
	resp := post(t, ts.URL+"/api/config", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if result["status"] != "valid" {
		t.Errorf("status: got %q, want \"valid\"", result["status"])
	}

	// File must NOT have been modified.
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Errorf("dry_run should not modify the file")
	}
}

func TestSaveConfigDryRunInvalid(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "")

	validate := func(_ []byte) []string {
		return []string{"error one", "error two"}
	}
	_, ts := newTestServer(t, path, validate)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"content": "bad yaml", "dry_run": true})
	resp := post(t, ts.URL+"/api/config", "application/json", body)

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	errs, _ := result["errors"].([]any)
	if len(errs) != 2 {
		t.Errorf("expected 2 errors, got %v", errs)
	}
}

// ── POST /api/config — save ───────────────────────────────────────────────────

func TestSaveConfigWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "old content\n")

	reloaded := false
	srv, ts := newTestServer(t, path, func(_ []byte) []string { return nil })
	srv.SetReload(func() error { reloaded = true; return nil })
	defer ts.Close()

	newContent := "new content\n"
	body, _ := json.Marshal(map[string]any{"content": newContent})
	resp := post(t, ts.URL+"/api/config", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	got, _ := os.ReadFile(path)
	if string(got) != newContent {
		t.Errorf("file content: got %q, want %q", string(got), newContent)
	}
	if !reloaded {
		t.Error("expected reload to be called when idle")
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if result["status"] != "reloaded" {
		t.Errorf("status: got %q, want \"reloaded\"", result["status"])
	}
}

func TestSaveConfigValidationError(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "original\n")
	original, _ := os.ReadFile(path)

	validate := func(_ []byte) []string { return []string{"plugin \"bad\": unknown"} }
	_, ts := newTestServer(t, path, validate)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"content": "invalid yaml here"})
	resp := post(t, ts.URL+"/api/config", "application/json", body)

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422", resp.StatusCode)
	}

	// File must be untouched.
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Error("validation failure should not modify the file")
	}
}

// ── POST /api/config — pending reload ─────────────────────────────────────────

func TestSaveConfigQueuesPendingReloadWhenBusy(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "old\n")

	reloadCalls := 0
	srv, ts := newTestServer(t, path, func(_ []byte) []string { return nil })
	srv.SetReload(func() error { reloadCalls++; return nil })
	defer ts.Close()

	// Simulate a running task.
	srv.TaskStarted("my-task")

	body, _ := json.Marshal(map[string]any{"content": "new\n"})
	resp := post(t, ts.URL+"/api/config", "application/json", body)
	defer resp.Body.Close()

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if result["status"] != "pending" {
		t.Errorf("status: got %q, want \"pending\"", result["status"])
	}
	if reloadCalls != 0 {
		t.Error("reload should not fire while task is running")
	}

	// Task finishes → reload fires automatically.
	srv.TaskDone("my-task")
	if reloadCalls != 1 {
		t.Errorf("expected 1 reload after task done, got %d", reloadCalls)
	}
}

func TestSaveConfigNoPendingReloadWithoutSave(t *testing.T) {
	srv, _ := newTestServer(t, "", nil)

	reloaded := false
	srv.SetReload(func() error { reloaded = true; return nil })

	srv.TaskStarted("task")
	srv.TaskDone("task")

	if reloaded {
		t.Error("should not reload when no config was saved")
	}
}

// ── POST /api/reload ──────────────────────────────────────────────────────────

func TestReloadBlockedWhileRunning(t *testing.T) {
	srv, ts := newTestServer(t, "", nil)
	srv.SetReload(func() error { return nil })
	defer ts.Close()

	srv.TaskStarted("task")
	resp := post(t, ts.URL+"/api/reload", "application/json", []byte{})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status: got %d, want 409", resp.StatusCode)
	}
	srv.TaskDone("task")
}

// ── SSE log replay / Last-Event-ID ───────────────────────────────────────────

func TestBroadcasterSequenceNumbers(t *testing.T) {
	b := NewBroadcaster()
	b.Write([]byte("line one\n"))   //nolint:errcheck
	b.Write([]byte("line two\n"))   //nolint:errcheck
	b.Write([]byte("line three\n")) //nolint:errcheck

	// Full snapshot: all three lines with ascending seq numbers.
	snap, ch := b.Subscribe(0)
	b.Unsubscribe(ch)
	if len(snap) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(snap))
	}
	for i, ll := range snap {
		if ll.seq != int64(i+1) {
			t.Errorf("snap[%d].seq = %d, want %d", i, ll.seq, i+1)
		}
	}
}

func TestBroadcasterResumeAfterSeq(t *testing.T) {
	b := NewBroadcaster()
	b.Write([]byte("line one\n"))   //nolint:errcheck
	b.Write([]byte("line two\n"))   //nolint:errcheck
	b.Write([]byte("line three\n")) //nolint:errcheck

	// Reconnect after having seen seq=2: should only get line three.
	snap, ch := b.Subscribe(2)
	b.Unsubscribe(ch)
	if len(snap) != 1 {
		t.Fatalf("expected 1 line after seq=2, got %d", len(snap))
	}
	if snap[0].text != "line three" {
		t.Errorf("text: got %q, want %q", snap[0].text, "line three")
	}
}

func TestBroadcasterNoReplayWhenFullyUpToDate(t *testing.T) {
	b := NewBroadcaster()
	b.Write([]byte("line one\n")) //nolint:errcheck
	b.Write([]byte("line two\n")) //nolint:errcheck

	// Client already has everything (afterSeq matches latest).
	snap, ch := b.Subscribe(2)
	b.Unsubscribe(ch)
	if len(snap) != 0 {
		t.Errorf("expected empty replay, got %d lines", len(snap))
	}
}

func TestSSELastEventIDHeaderParsed(t *testing.T) {
	b := NewBroadcaster()
	b.Write([]byte("line one\n")) //nolint:errcheck
	b.Write([]byte("line two\n")) //nolint:errcheck

	srv := New(nil, stubDaemon{}, NewHistory(), b, "test", "user", "pass")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/logs", srv.apiLogs)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Request with Last-Event-ID: 1 — should only stream "line two".
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/logs", nil)
	req.Header.Set("Last-Event-ID", "1")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	if strings.Contains(body, "line one") {
		t.Error("line one should not be replayed after Last-Event-ID: 1")
	}
	if !strings.Contains(body, "line two") {
		t.Errorf("line two should be replayed, got: %s", body)
	}
}

// ── GET /api/plugins ─────────────────────────────────────────────────────────

func TestAPIPluginsReturnsArray(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugins", srv.apiPlugins)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/plugins")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}
	var plugins []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&plugins); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(plugins) == 0 {
		t.Error("expected at least one plugin, got none")
	}
	// Every entry must have name, role, description, schema.
	for _, p := range plugins {
		for _, field := range []string{"name", "role", "description", "schema"} {
			if _, ok := p[field]; !ok {
				t.Errorf("plugin %v missing field %q", p["name"], field)
			}
		}
	}
}

func TestAPIPluginsSchemaIsArrayNotNull(t *testing.T) {
	// schema must be [] not null even for plugins without a declared schema.
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugins", srv.apiPlugins)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/plugins")
	defer resp.Body.Close()

	var plugins []struct {
		Schema []any `json:"schema"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&plugins); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i, p := range plugins {
		if p.Schema == nil {
			t.Errorf("plugins[%d].schema is null, want []", i)
		}
	}
}

// ── POST /api/config/parse ───────────────────────────────────────────────────

func newParseServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/config/parse", srv.apiConfigParse)
	return httptest.NewServer(mux)
}

func TestAPIConfigParseValidStarlark(t *testing.T) {
	ts := newParseServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{
		"content": `
src = input("rss", url="https://example.com")
output("print", from_=src)
pipeline("tv", schedule="1h")
`,
	})
	resp := post(t, ts.URL+"/api/config/parse", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	graphs, ok := result["graphs"].(map[string]any)
	if !ok {
		t.Fatalf("graphs missing or wrong type: %v", result["graphs"])
	}
	tv, ok := graphs["tv"].(map[string]any)
	if !ok {
		t.Fatalf("pipeline 'tv' missing: %v", graphs)
	}
	if tv["schedule"] != "1h" {
		t.Errorf("schedule: got %v, want 1h", tv["schedule"])
	}
	nodes, _ := tv["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes: got %d, want 2 (rss + print)", len(nodes))
	}
}

func TestAPIConfigParseInvalidStarlark(t *testing.T) {
	ts := newParseServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"content": `def broken(`})
	resp := post(t, ts.URL+"/api/config/parse", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422", resp.StatusCode)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["error"] == "" {
		t.Error("expected error field in response")
	}
}

func TestAPIConfigParseFlattensFunctions(t *testing.T) {
	// Starlark functions compose DAG chains — the parsed result shows all nodes.
	ts := newParseServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"content": `
feed = "https://example.com/rss"
def common(upstream):
    return process("seen", from_=upstream)
src = input("rss", url=feed)
flt = common(src)
output("print", from_=flt)
pipeline("t")
`})
	resp := post(t, ts.URL+"/api/config/parse", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var result struct {
		Graphs map[string]struct {
			Nodes []struct{ Plugin string `json:"plugin"` } `json:"nodes"`
		} `json:"graphs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Graphs["t"].Nodes) != 3 {
		t.Errorf("want 3 nodes (rss, seen, print) after function expansion, got %d", len(result.Graphs["t"].Nodes))
	}
}

func TestAPIConfigParseBadJSON(t *testing.T) {
	ts := newParseServer(t)
	defer ts.Close()

	resp := post(t, ts.URL+"/api/config/parse", "application/json", []byte("not json"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

// ── static asset serving ─────────────────────────────────────────────────────

func TestStaticCSSServed(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", srv.serveUI) // exact root only
	mux.Handle("/", srv.staticHandler())    // catch-all for assets
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := get(t, ts.URL+"/style.css")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("style.css: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("content-type: got %q, want css", ct)
	}
}

func TestStaticJSServed(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", srv.serveUI)
	mux.Handle("/", srv.staticHandler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	for _, file := range []string{"dashboard.js", "visual-editor.js", "highlight.js"} {
		resp := get(t, ts.URL+"/"+file)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: got %d, want 200", file, resp.StatusCode)
		}
	}
}
