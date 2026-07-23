package transmission

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/grabs"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// --- helpers ---

type rpcRequest struct {
	Method    string         `json:"method"`
	Arguments map[string]any `json:"arguments"`
}

// mockTransmission serves the Transmission RPC protocol:
// first call per session → 409 with session id; subsequent calls → 200.
func mockTransmission(t *testing.T, handler func(rpcRequest) any) *httptest.Server {
	t.Helper()
	const sessionID = "test-session-id-123"
	var ready atomic.Bool

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			ready.Store(true)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		result := handler(req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"result":    "success",
			"arguments": result,
		})
	}))
}

func pluginWithEndpoint(t *testing.T, srv *httptest.Server, cfg map[string]any) *transmissionPlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	tp := p.(*transmissionPlugin)
	tp.endpoint = srv.URL + "/transmission/rpc"
	return tp
}

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// --- tests ---

func TestAddTorrentBasic(t *testing.T) {
	var captured rpcRequest
	srv := mockTransmission(t, func(req rpcRequest) any {
		captured = req
		return map[string]any{"torrent-added": map[string]any{"id": 1}}
	})
	defer srv.Close()

	p := pluginWithEndpoint(t, srv, map[string]any{"path": "/media/tv/{title}"})

	e := entry.New("My.Show.S01E01", "http://tracker.example/file.torrent")
	e.Set("title", "My Show")

	if err := p.deliver(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	if captured.Method != "torrent-add" {
		t.Errorf("method: got %q, want torrent-add", captured.Method)
	}
	if captured.Arguments["filename"] != "http://tracker.example/file.torrent" {
		t.Errorf("filename: got %v", captured.Arguments["filename"])
	}
	if captured.Arguments["download-dir"] != "/media/tv/My Show" {
		t.Errorf("download-dir: got %v", captured.Arguments["download-dir"])
	}
}

func TestSessionIDHandshake(t *testing.T) {
	callCount := 0
	srv := mockTransmission(t, func(_ rpcRequest) any {
		callCount++
		return nil
	})
	defer srv.Close()

	p := pluginWithEndpoint(t, srv, map[string]any{})

	e := entry.New("test", "http://example.com/test.torrent")
	if err := p.deliver(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("rpc handler called %d times, want 1", callCount)
	}
}

func TestPausedConfig(t *testing.T) {
	var captured rpcRequest
	srv := mockTransmission(t, func(req rpcRequest) any {
		captured = req
		return nil
	})
	defer srv.Close()

	p := pluginWithEndpoint(t, srv, map[string]any{"paused": true})

	e := entry.New("x", "http://example.com/x.torrent")
	p.deliver(context.Background(), tc(), []*entry.Entry{e})

	if captured.Arguments["paused"] != true {
		t.Errorf("paused: got %v, want true", captured.Arguments["paused"])
	}
}

func TestPathTemplate(t *testing.T) {
	var captured rpcRequest
	srv := mockTransmission(t, func(req rpcRequest) any {
		captured = req
		return nil
	})
	defer srv.Close()

	p := pluginWithEndpoint(t, srv, map[string]any{
		"path": "/media/{{.category}}/{{.Title}}",
	})

	e := entry.New("My.Show.S01E01", "http://example.com/show.torrent")
	e.Set("category", "tv")

	p.deliver(context.Background(), tc(), []*entry.Entry{e})

	if captured.Arguments["download-dir"] != "/media/tv/My.Show.S01E01" {
		t.Errorf("download-dir: got %v", captured.Arguments["download-dir"])
	}
}

func TestDefaultConfig(t *testing.T) {
	p, err := newPlugin(map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tp := p.(*transmissionPlugin)
	if tp.endpoint != "http://localhost:9091/transmission/rpc" {
		t.Errorf("endpoint: got %q", tp.endpoint)
	}
	if tp.paused {
		t.Error("paused should default to false")
	}
}

func TestInvalidPathTemplate(t *testing.T) {
	_, err := newPlugin(map[string]any{"path": "{{invalid"}, nil)
	if err == nil {
		t.Error("expected error for invalid path template")
	}
}

func TestPluginRegistered(t *testing.T) {
	if _, ok := plugin.Lookup("transmission"); !ok {
		t.Error("transmission not registered")
	}
}

func TestMultipleEntries(t *testing.T) {
	var calls []string
	srv := mockTransmission(t, func(req rpcRequest) any {
		calls = append(calls, req.Arguments["filename"].(string))
		return nil
	})
	defer srv.Close()

	p := pluginWithEndpoint(t, srv, map[string]any{})

	entries := []*entry.Entry{
		entry.New("e1", "http://example.com/a.torrent"),
		entry.New("e2", "http://example.com/b.torrent"),
		entry.New("e3", "http://example.com/c.torrent"),
	}
	if err := p.deliver(context.Background(), tc(), entries); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Errorf("want 3 torrent-add calls, got %d", len(calls))
	}
}

func TestGrabRecordedOnSuccessfulAdd(t *testing.T) {
	srv := mockTransmission(t, func(req rpcRequest) any {
		return map[string]any{"torrent-added": map[string]any{
			"id":         1,
			"hashString": "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		}}
	})
	defer srv.Close()

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	p, err := newPlugin(map[string]any{}, db)
	if err != nil {
		t.Fatal(err)
	}
	tp := p.(*transmissionPlugin)
	tp.endpoint = srv.URL + "/transmission/rpc"

	e := entry.New("Show.S01E03.720p", "http://tracker.example/42.torrent")
	e.Set(entry.FieldSeriesTrackerName, "show")
	e.Set(entry.FieldSeriesEpisodeID, "S01E03")

	if err := tp.deliver(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	rec, ok := gs.Get("abcdef0123456789abcdef0123456789abcdef01")
	if !ok {
		t.Fatal("grab record should exist after successful add")
	}
	if rec.URL != "http://tracker.example/42.torrent" {
		t.Errorf("rec.URL = %q", rec.URL)
	}
	if rec.SeriesName != "show" || rec.EpisodeID != "S01E03" {
		t.Errorf("series key = %q/%q", rec.SeriesName, rec.EpisodeID)
	}
	if rec.Task != "test" {
		t.Errorf("rec.Task = %q", rec.Task)
	}
}

func TestGrabRecordedFromDuplicateResponse(t *testing.T) {
	srv := mockTransmission(t, func(req rpcRequest) any {
		return map[string]any{"torrent-duplicate": map[string]any{
			"id":         7,
			"hashString": "1111111111111111111111111111111111111111",
		}}
	})
	defer srv.Close()

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	p, err := newPlugin(map[string]any{}, db)
	if err != nil {
		t.Fatal(err)
	}
	tp := p.(*transmissionPlugin)
	tp.endpoint = srv.URL + "/transmission/rpc"

	e := entry.New("Dup", "http://tracker.example/dup.torrent")
	if err := tp.deliver(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if _, ok := gs.Get("1111111111111111111111111111111111111111"); !ok {
		t.Fatal("grab record should exist for duplicate add")
	}
}

func TestNoGrabRecordOnFailedAdd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	p, err := newPlugin(map[string]any{}, db)
	if err != nil {
		t.Fatal(err)
	}
	tp := p.(*transmissionPlugin)
	tp.endpoint = srv.URL + "/transmission/rpc"

	e := entry.New("Bad", "magnet:?xt=urn:btih:2222222222222222222222222222222222222222")
	if err := tp.deliver(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsFailed() {
		t.Fatal("entry should be failed")
	}
	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if _, ok := gs.Get("2222222222222222222222222222222222222222"); ok {
		t.Error("no grab record should be written for a failed add")
	}
}
