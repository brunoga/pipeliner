package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// Register a handful of plugins so plugin.All() is non-empty in tests.
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/processor/modify/pathfmt"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
)

// stubDaemon satisfies DaemonControl with no-op implementations.
type stubDaemon struct{}

func (s stubDaemon) NextRun(_ string) time.Time { return time.Time{} }
func (s stubDaemon) Trigger(_ string, _ bool)   {}

// newTestServer builds a Server wired for testing.
// configPath and validateFn are optional; pass "" / nil to skip.
func newTestServer(t *testing.T, configPath string, validateFn func([]byte) ([]string, []string)) (*Server, *httptest.Server) {
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

	_, ts := newTestServer(t, path, func(_ []byte) ([]string, []string) { return nil, nil })
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"content": "new content\n", "dry_run": true})
	resp := post(t, ts.URL+"/api/config", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
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

	validate := func(_ []byte) ([]string, []string) {
		return []string{"error one", "error two"}, nil
	}
	_, ts := newTestServer(t, path, validate)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"content": "bad yaml", "dry_run": true})
	resp := post(t, ts.URL+"/api/config", "application/json", body)

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
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
	srv, ts := newTestServer(t, path, func(_ []byte) ([]string, []string) { return nil, nil })
	// Production reload writes the file atomically as part of the commit step;
	// the test stub mirrors that contract so apiSaveConfig's "the file got
	// updated after a successful save" guarantee can be observed end-to-end.
	srv.SetReload(func(content []byte) error {
		reloaded = true
		return os.WriteFile(path, content, 0600)
	})
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
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "reloaded" {
		t.Errorf("status: got %q, want \"reloaded\"", result["status"])
	}
}

// TestSaveConfigPreservesFileOnReloadError verifies that a reload error
// (e.g. plugin Factory rejecting a structurally-valid config) leaves the
// on-disk file untouched. Previously the handler wrote the file before
// calling reload, so a Factory error left a broken config persisted that
// would block the next startup.
func TestSaveConfigPreservesFileOnReloadError(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "original content\n")
	original, _ := os.ReadFile(path)

	srv, ts := newTestServer(t, path, func(_ []byte) ([]string, []string) { return nil, nil })
	srv.SetReload(func([]byte) error {
		// Simulate the production reload's contract: if Factory fails, the
		// reload function returns an error without touching disk.
		return errors.New("plugin factory error")
	})
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"content": "new content that builds-fails\n"})
	resp := post(t, ts.URL+"/api/config", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422 (reload error)", resp.StatusCode)
	}

	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Errorf("file content changed despite reload failure: got %q, want %q", got, original)
	}
}

// TestSaveConfigRejectsOversizedBody verifies the 1 MB body cap. Without it
// a malicious LAN client could buffer arbitrary amounts of memory by posting
// a huge JSON body.
func TestSaveConfigRejectsOversizedBody(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "x")
	_, ts := newTestServer(t, path, func(_ []byte) ([]string, []string) { return nil, nil })
	defer ts.Close()

	// 2 MB of payload — the JSON object wrapping it is also charged against
	// the limit, but the content alone is already over the cap.
	huge := bytes.Repeat([]byte("a"), 2<<20)
	body, _ := json.Marshal(map[string]any{"content": string(huge)})
	resp := post(t, ts.URL+"/api/config", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (body too large should fail JSON decode)", resp.StatusCode)
	}
}

func TestSaveConfigValidationError(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "original\n")
	original, _ := os.ReadFile(path)

	validate := func(_ []byte) ([]string, []string) { return []string{"plugin \"bad\": unknown"}, nil }
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
	var reloadBytes []byte
	srv, ts := newTestServer(t, path, func(_ []byte) ([]string, []string) { return nil, nil })
	srv.SetReload(func(content []byte) error {
		reloadCalls++
		reloadBytes = content
		return nil
	})
	defer ts.Close()

	// Simulate a running task.
	srv.TaskStarted("my-task")

	body, _ := json.Marshal(map[string]any{"content": "new\n"})
	resp := post(t, ts.URL+"/api/config", "application/json", body)
	defer resp.Body.Close()

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "pending" {
		t.Errorf("status: got %q, want \"pending\"", result["status"])
	}
	if reloadCalls != 0 {
		t.Error("reload should not fire while task is running")
	}

	// Task finishes → reload fires automatically with the queued bytes.
	srv.TaskDone("my-task")
	if reloadCalls != 1 {
		t.Errorf("expected 1 reload after task done, got %d", reloadCalls)
	}
	if string(reloadBytes) != "new\n" {
		t.Errorf("queued reload bytes: got %q, want %q", reloadBytes, "new\n")
	}
}

func TestSaveConfigNoPendingReloadWithoutSave(t *testing.T) {
	srv, _ := newTestServer(t, "", nil)

	reloaded := false
	srv.SetReload(func([]byte) error { reloaded = true; return nil })

	srv.TaskStarted("task")
	srv.TaskDone("task")

	if reloaded {
		t.Error("should not reload when no config was saved")
	}
}

// ── POST /api/reload ──────────────────────────────────────────────────────────

func TestReloadBlockedWhileRunning(t *testing.T) {
	srv, ts := newTestServer(t, "", nil)
	srv.SetReload(func([]byte) error { return nil })
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
	b.Publish("line one", 9)
	b.Publish("line two", 18)
	b.Publish("line three", 29)

	// Full snapshot: all three lines with ascending seq numbers.
	snap, ch, _ := b.Subscribe(0)
	b.Unsubscribe(ch)
	if len(snap) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(snap))
	}
	for i, ev := range snap {
		if ev.Seq != int64(i+1) {
			t.Errorf("snap[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

func TestBroadcasterResumeAfterSeq(t *testing.T) {
	b := NewBroadcaster()
	b.Publish("line one", 9)
	b.Publish("line two", 18)
	b.Publish("line three", 29)

	// Reconnect after having seen seq=2: should only get line three.
	snap, ch, _ := b.Subscribe(2)
	b.Unsubscribe(ch)
	if len(snap) != 1 {
		t.Fatalf("expected 1 line after seq=2, got %d", len(snap))
	}
	if snap[0].Text != "line three" {
		t.Errorf("Text: got %q, want %q", snap[0].Text, "line three")
	}
}

func TestBroadcasterNoReplayWhenFullyUpToDate(t *testing.T) {
	b := NewBroadcaster()
	b.Publish("line one", 9)
	b.Publish("line two", 18)

	// Client already has everything (afterSeq matches latest).
	snap, ch, _ := b.Subscribe(2)
	b.Unsubscribe(ch)
	if len(snap) != 0 {
		t.Errorf("expected empty replay, got %d lines", len(snap))
	}
}

func TestBroadcasterNotifyRotateBumpsFileIdx(t *testing.T) {
	// Lines published before a rotation must surface to a reconnecting
	// client at their post-rotation file index (now 1 since rotation
	// pushed them out of the base file).
	b := NewBroadcaster()
	b.Publish("pre-rotate", 11)
	b.NotifyRotate()

	snap, ch, rot := b.Subscribe(0)
	b.Unsubscribe(ch)
	if rot != 1 {
		t.Errorf("rotationSeq = %d, want 1", rot)
	}
	if len(snap) != 1 {
		t.Fatalf("snap len = %d, want 1", len(snap))
	}
	if snap[0].Pos.FileIdx != 1 {
		t.Errorf("FileIdx = %d after rotation, want 1", snap[0].Pos.FileIdx)
	}
}

func TestSSELastEventIDHeaderParsed(t *testing.T) {
	b := NewBroadcaster()
	b.Publish("line one", 9)
	b.Publish("line two", 18)

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

func TestSSERotateEventDeliveredToLiveSubscribers(t *testing.T) {
	// A rotation that happens while a client is actively connected must
	// surface as an `event: rotate` SSE frame so the client knows to
	// refresh its in-memory cursors. The new file's first line then
	// streams as a regular message event with FileIdx 0.
	b := NewBroadcaster()
	srv := New(nil, stubDaemon{}, NewHistory(), b, "test", "user", "pass")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/logs", srv.apiLogs)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/logs", nil)
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Drive the broadcaster: pre-rotation line, rotate, post-rotation line.
	// Tiny delay so the request handler has subscribed before we start.
	go func() {
		time.Sleep(50 * time.Millisecond)
		b.Publish("pre-rotate", 11)
		b.NotifyRotate()
		b.Publish("post-rotate", 12)
	}()

	// Drain the SSE stream until we've seen both the rotate frame and
	// the post-rotate payload, or the connection times out.
	var body string
	deadline := time.Now().Add(400 * time.Millisecond)
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body += string(buf[:n])
			if strings.Contains(body, "event: rotate") && strings.Contains(body, "post-rotate") {
				break
			}
		}
		if err != nil {
			break
		}
	}

	if !strings.Contains(body, "event: rotate") {
		t.Errorf("expected 'event: rotate' frame in stream, got:\n%s", body)
	}
	if !strings.Contains(body, "post-rotate") {
		t.Errorf("expected post-rotate line after rotation, got:\n%s", body)
	}
	// post-rotate's payload should carry the fresh-file cursor 0:12.
	if !strings.Contains(body, `"pos":"0:12"`) {
		t.Errorf("expected post-rotate pos in stream, got:\n%s", body)
	}
}

func TestSSEServerFilteredByQ(t *testing.T) {
	// A line that matches and a line that doesn't both go through the
	// broadcaster; the SSE handler only emits the matching one when ?q=
	// is set.
	b := NewBroadcaster()
	b.Publish("match: pipeline tv done", 24)
	b.Publish("skip: cache miss", 41)

	srv := New(nil, stubDaemon{}, NewHistory(), b, "test", "user", "pass")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/logs", srv.apiLogs)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/logs?q=tv", nil)
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
	if !strings.Contains(body, "pipeline tv done") {
		t.Errorf("want match present, body=%s", body)
	}
	if strings.Contains(body, "cache miss") {
		t.Errorf("filter let non-matching line through: %s", body)
	}
}

// ── GET /api/logs/{tail,before,after} ─────────────────────────────────────────
//
// The viewer is a sliding window over the rotating log set. The endpoints
// share a JSON shape carrying lines + opaque cursors. Each test seeds a
// temp log file (and archives when relevant) then issues a single GET to
// lock down the contract.

func writeLogLines(t *testing.T, path string, lines []string) {
	t.Helper()
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}

func setupLogServer(t *testing.T, logPath string, archives int) *httptest.Server {
	t.Helper()
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	srv.SetLogFile(logPath, archives)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/logs/tail", srv.apiLogsTail)
	mux.HandleFunc("GET /api/logs/before", srv.apiLogsBefore)
	mux.HandleFunc("GET /api/logs/after", srv.apiLogsAfter)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

type logRespDecoded struct {
	Lines       []LineWithPos `json:"lines"`
	OlderCursor string        `json:"older_cursor"`
	NewerCursor string        `json:"newer_cursor"`
	Exhausted   bool          `json:"exhausted"`
	AtTail      bool          `json:"at_tail"`
}

func decodeLogResp(t *testing.T, resp *http.Response) logRespDecoded {
	t.Helper()
	var body logRespDecoded
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func lineTexts(lwps []LineWithPos) []string {
	out := make([]string, len(lwps))
	for i, l := range lwps {
		out[i] = l.Text
	}
	return out
}

func TestAPILogsTailReturnsNewest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")
	writeLogLines(t, path, []string{"a", "b", "c", "d", "e"})
	ts := setupLogServer(t, path, 0)

	resp := get(t, ts.URL+"/api/logs/tail?limit=3")
	defer resp.Body.Close()
	body := decodeLogResp(t, resp)
	if want := []string{"c", "d", "e"}; !equalStrings(lineTexts(body.Lines), want) {
		t.Errorf("got %v, want %v", lineTexts(body.Lines), want)
	}
	if body.Exhausted {
		t.Error("want exhausted=false (older lines exist)")
	}
	if body.OlderCursor == "" {
		t.Error("want older_cursor when not exhausted")
	}
}

func TestAPILogsTailExhaustedShortFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")
	writeLogLines(t, path, []string{"a", "b"})
	ts := setupLogServer(t, path, 0)

	resp := get(t, ts.URL+"/api/logs/tail?limit=10")
	defer resp.Body.Close()
	body := decodeLogResp(t, resp)
	if want := []string{"a", "b"}; !equalStrings(lineTexts(body.Lines), want) {
		t.Errorf("texts = %v, want %v", lineTexts(body.Lines), want)
	}
	if !body.Exhausted {
		t.Error("want exhausted=true")
	}
}

func TestAPILogsBeforePagesOlder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")
	writeLogLines(t, path, []string{"a", "b", "c", "d", "e"})
	ts := setupLogServer(t, path, 0)

	tail := decodeLogResp(t, mustGet(t, ts.URL+"/api/logs/tail?limit=2"))
	if want := []string{"d", "e"}; !equalStrings(lineTexts(tail.Lines), want) {
		t.Fatalf("tail = %v, want %v", lineTexts(tail.Lines), want)
	}

	before := decodeLogResp(t, mustGet(t,
		ts.URL+"/api/logs/before?cursor="+tail.OlderCursor+"&limit=2"))
	if want := []string{"b", "c"}; !equalStrings(lineTexts(before.Lines), want) {
		t.Fatalf("before = %v, want %v", lineTexts(before.Lines), want)
	}
}

func TestAPILogsTailFilteredAcrossArchives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")
	writeLogLines(t, path+".1", []string{"INFO pipeline=tv started", "DEBUG misc"})
	writeLogLines(t, path, []string{"INFO pipeline=movies done", "INFO pipeline=tv done"})
	ts := setupLogServer(t, path, 5)

	resp := get(t, ts.URL+"/api/logs/tail?limit=10&q=INFO+tv")
	defer resp.Body.Close()
	body := decodeLogResp(t, resp)
	want := []string{"INFO pipeline=tv started", "INFO pipeline=tv done"}
	if !equalStrings(lineTexts(body.Lines), want) {
		t.Errorf("got %v, want %v", lineTexts(body.Lines), want)
	}
}

func TestAPILogsAfterReachesTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")
	writeLogLines(t, path, []string{"a", "b", "c", "d", "e"})
	ts := setupLogServer(t, path, 0)

	// Take the full file to obtain a cursor for 'a'.
	full := decodeLogResp(t, mustGet(t, ts.URL+"/api/logs/tail?limit=5"))
	if len(full.Lines) != 5 {
		t.Fatalf("seed: got %d lines, want 5", len(full.Lines))
	}
	posOfA := full.Lines[0].Pos.String()

	resp := decodeLogResp(t, mustGet(t,
		ts.URL+"/api/logs/after?cursor="+posOfA+"&limit=10"))
	if want := []string{"b", "c", "d", "e"}; !equalStrings(lineTexts(resp.Lines), want) {
		t.Errorf("got %v, want %v", lineTexts(resp.Lines), want)
	}
	if !resp.AtTail {
		t.Error("want at_tail=true after consuming base file")
	}
}

func TestAPILogsTailLimitClamped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")
	lines := make([]string, logHistoryMaxLimit+50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	writeLogLines(t, path, lines)
	ts := setupLogServer(t, path, 0)

	resp := get(t, ts.URL+"/api/logs/tail?limit=99999")
	defer resp.Body.Close()
	body := decodeLogResp(t, resp)
	if len(body.Lines) != logHistoryMaxLimit {
		t.Errorf("len = %d, want %d (clamp)", len(body.Lines), logHistoryMaxLimit)
	}
}

func TestAPILogsTailNoLogFileConfigured(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/logs/tail", srv.apiLogsTail)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/logs/tail?limit=10")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeLogResp(t, resp)
	if len(body.Lines) != 0 {
		t.Errorf("lines = %v, want empty", body.Lines)
	}
	if !body.Exhausted {
		t.Error("want exhausted=true when no file configured")
	}
}

func TestAPILogsBeforeBadCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")
	writeLogLines(t, path, []string{"a"})
	ts := setupLogServer(t, path, 0)

	resp := get(t, ts.URL+"/api/logs/before?cursor=nope&limit=1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	r := get(t, url)
	t.Cleanup(func() { r.Body.Close() })
	return r
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
output("print", upstream=src)
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

// TestAPIConfigParseReturnsGraphOrder is the regression test for the
// dashboard/visual-editor ordering bug: the JSON encoder sorts the
// graphs map by key, so without an explicit graph_order the client
// would always render pipelines alphabetically. The response now
// carries graph_order in source order so the visual editor can match
// the text editor's layout.
func TestAPIConfigParseReturnsGraphOrder(t *testing.T) {
	ts := newParseServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{
		"content": `
# Names chosen so alphabetical order != source order.
src1 = input("rss", url="https://z.example/rss")
output("print", upstream=src1)
pipeline("zulu")

src2 = input("rss", url="https://a.example/rss")
output("print", upstream=src2)
pipeline("alpha")

src3 = input("rss", url="https://m.example/rss")
output("print", upstream=src3)
pipeline("mike")
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
	rawOrder, ok := result["graph_order"].([]any)
	if !ok {
		t.Fatalf("graph_order missing or wrong type: %v", result["graph_order"])
	}
	want := []string{"zulu", "alpha", "mike"}
	if len(rawOrder) != len(want) {
		t.Fatalf("graph_order length: got %d (%v), want %d (%v)", len(rawOrder), rawOrder, len(want), want)
	}
	for i, name := range want {
		if rawOrder[i] != name {
			t.Errorf("graph_order[%d]: got %v, want %q", i, rawOrder[i], name)
		}
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
    return process("seen", upstream=upstream)
src = input("rss", url=feed)
flt = common(src)
output("print", upstream=flt)
pipeline("t")
`})
	resp := post(t, ts.URL+"/api/config/parse", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var result struct {
		Graphs map[string]struct {
			Nodes []struct {
				Plugin string `json:"plugin"`
			} `json:"nodes"`
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

// ── scanComments unit tests ───────────────────────────────────────────────────

func TestScanCommentsNodeComment(t *testing.T) {
	nc, _, _ := scanComments("# Main source\nsrc_0 = input(\"rss\", url=\"https://example.com\")\npipeline(\"tv\")\n")
	if nc["src_0"] != "Main source" {
		t.Errorf("node comment: got %q, want %q", nc["src_0"], "Main source")
	}
}

func TestScanCommentsMultilineNodeComment(t *testing.T) {
	nc, _, _ := scanComments("# Line one\n# Line two\nsrc_0 = input(\"rss\")\npipeline(\"tv\")\n")
	want := "Line one\nLine two"
	if nc["src_0"] != want {
		t.Errorf("multiline comment: got %q, want %q", nc["src_0"], want)
	}
}

func TestScanCommentsProcessorNodeComment(t *testing.T) {
	nc, _, _ := scanComments("src = input(\"rss\")\n# Deduplicate\nseen_1 = process(\"seen\", upstream=src)\npipeline(\"p\")\n")
	if nc["seen_1"] != "Deduplicate" {
		t.Errorf("processor comment: got %q, want %q", nc["seen_1"], "Deduplicate")
	}
}

func TestScanCommentsPipelineComment(t *testing.T) {
	_, pc, _ := scanComments("src = input(\"rss\")\n# TV shows pipeline\npipeline(\"tv\")\n")
	if pc["tv"] != "TV shows pipeline" {
		t.Errorf("pipeline comment: got %q, want %q", pc["tv"], "TV shows pipeline")
	}
}

func TestScanCommentsPerNodePos(t *testing.T) {
	_, _, pos := scanComments("# pipeliner:pos 50 32\nsrc_0 = input(\"rss\")\npipeline(\"tv\")\n")
	p, ok := pos["src_0"]
	if !ok {
		t.Fatal("position missing for src_0")
	}
	if p.Main[0] != 50 || p.Main[1] != 32 {
		t.Errorf("position: got [%v %v], want [50 32]", p.Main[0], p.Main[1])
	}
}

func TestScanCommentsPosWithSubNodes(t *testing.T) {
	content := "# pipeliner:pos 50 32 list 10 5 20 6 search 30 7\nsrc_0 = input(\"rss\")\npipeline(\"tv\")\n"
	_, _, pos := scanComments(content)
	p, ok := pos["src_0"]
	if !ok {
		t.Fatal("position missing for src_0")
	}
	if p.Main[0] != 50 || p.Main[1] != 32 {
		t.Errorf("main position: got [%v %v], want [50 32]", p.Main[0], p.Main[1])
	}
	if len(p.List) != 2 || p.List[0] != [2]float64{10, 5} || p.List[1] != [2]float64{20, 6} {
		t.Errorf("list positions: got %v, want [{10 5} {20 6}]", p.List)
	}
	if len(p.Search) != 1 || p.Search[0] != [2]float64{30, 7} {
		t.Errorf("search positions: got %v, want [{30 7}]", p.Search)
	}
}

func TestScanCommentsPosWithUserComment(t *testing.T) {
	nc, _, pos := scanComments("# My source\n# pipeliner:pos 50 32\nsrc_0 = input(\"rss\")\npipeline(\"tv\")\n")
	if nc["src_0"] != "My source" {
		t.Errorf("node comment: got %q", nc["src_0"])
	}
	p, ok := pos["src_0"]
	if !ok {
		t.Fatal("position missing for src_0")
	}
	if p.Main[0] != 50 || p.Main[1] != 32 {
		t.Errorf("position: got [%v %v], want [50 32]", p.Main[0], p.Main[1])
	}
}

func TestScanCommentsLegacyLayoutBackwardCompat(t *testing.T) {
	// Old pipeliner:layout format must still be parsed for backward compatibility.
	_, _, pos := scanComments("src_0 = input(\"rss\")\n# pipeliner:layout {\"src_0\":[50,76]}\npipeline(\"tv\")\n")
	p, ok := pos["src_0"]
	if !ok {
		t.Fatal("legacy layout position missing for src_0")
	}
	if p.Main[0] != 50 || p.Main[1] != 76 {
		t.Errorf("legacy layout position: got [%v %v], want [50 76]", p.Main[0], p.Main[1])
	}
}

func TestScanCommentsPosDoesNotCrossPipelineBoundary(t *testing.T) {
	// A pipeliner:pos comment at the end of pipeline A must not be attributed
	// to the first node of pipeline B.
	content := "src_a = input(\"rss\")\n# pipeliner:pos 10 20\npipeline(\"a\")\n\nsrc_b = input(\"rss\")\npipeline(\"b\")\n"
	_, _, pos := scanComments(content)
	if _, ok := pos["src_b"]; ok {
		t.Error("pos from pipeline A must not leak into pipeline B")
	}
}

func TestScanCommentsBlankLineResetsComment(t *testing.T) {
	nc, _, _ := scanComments("# Lost comment\n\nsrc_0 = input(\"rss\")\npipeline(\"tv\")\n")
	if nc["src_0"] != "" {
		t.Errorf("blank line should reset comment, got %q", nc["src_0"])
	}
}

func TestScanCommentsNoAnnotations(t *testing.T) {
	nc, pc, pos := scanComments("src = input(\"rss\")\npipeline(\"tv\")\n")
	if len(nc) != 0 || len(pc) != 0 || len(pos) != 0 {
		t.Errorf("expected empty maps, got nc=%v pc=%v pos=%v", nc, pc, pos)
	}
}

func TestScanCommentsMultiplePipelines(t *testing.T) {
	content := "# Source A\na_0 = input(\"rss\")\n# Pipeline A\npipeline(\"a\")\n\n# Source B\nb_0 = input(\"rss\")\n# Pipeline B\npipeline(\"b\")\n"
	nc, pc, _ := scanComments(content)
	if nc["a_0"] != "Source A" {
		t.Errorf("a_0: got %q", nc["a_0"])
	}
	if nc["b_0"] != "Source B" {
		t.Errorf("b_0: got %q", nc["b_0"])
	}
	if pc["a"] != "Pipeline A" {
		t.Errorf("pipeline a: got %q", pc["a"])
	}
	if pc["b"] != "Pipeline B" {
		t.Errorf("pipeline b: got %q", pc["b"])
	}
}

func TestAPIConfigParseReturnsCommentAndPos(t *testing.T) {
	ts := newParseServer(t)
	defer ts.Close()

	// Variable name must match generated node ID (plugin_N format) for the
	// scanner to associate the comment correctly.
	body, _ := json.Marshal(map[string]string{
		"content": "# RSS source\n# pipeliner:pos 50 76\nrss_0 = input(\"rss\", url=\"https://example.com\")\n# My pipeline\npipeline(\"tv\")\n",
	})
	resp := post(t, ts.URL+"/api/config/parse", "application/json", body)
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	graphs := result["graphs"].(map[string]any)
	tv := graphs["tv"].(map[string]any)

	if tv["comment"] != "My pipeline" {
		t.Errorf("pipeline comment: got %v", tv["comment"])
	}
	if _, ok := tv["layout"]; ok {
		t.Error("layout field must not appear in graph response — positions are per-node")
	}
	nodes := tv["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("no nodes")
	}
	node := nodes[0].(map[string]any)
	if node["comment"] != "RSS source" {
		t.Errorf("node comment: got %v", node["comment"])
	}
	if node["x"].(float64) != 50 || node["y"].(float64) != 76 {
		t.Errorf("node pos: got x=%v y=%v, want 50 76", node["x"], node["y"])
	}
}

func TestAPIPluginsExcludesInternalPlugins(t *testing.T) {
	// Internal plugins (e.g. route_selector) must never appear in the palette
	// response even though they are registered in the plugin registry.
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugins", srv.apiPlugins)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/plugins")
	defer resp.Body.Close()

	var plugins []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&plugins); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, p := range plugins {
		if p["name"] == "route_selector" {
			t.Error("route_selector is an internal plugin and must not appear in /api/plugins")
		}
	}
}
