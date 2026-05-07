package magnet

import (
	"context"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

const (
	hexHash = "aabbccddeeff00112233445566778899aabbccdd"
	tracker = "http://tracker.example.com/announce"
)

func taskCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Logger: slog.Default()}
}

func annotate(t *testing.T, e *entry.Entry) {
	t.Helper()
	p := &magnetPlugin{}
	if err := p.Annotate(context.Background(), taskCtx(), e); err != nil {
		t.Fatalf("Annotate: %v", err)
	}
}

func TestAnnotatesMagnetURL(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash + "&tr=" + tracker
	e := entry.New("some title", uri)
	annotate(t, e)

	if v := e.GetString("torrent_info_hash"); v != hexHash {
		t.Errorf("info_hash: got %q, want %q", v, hexHash)
	}
	if v := e.GetString("torrent_announce"); v != tracker {
		t.Errorf("announce: got %q, want %q", v, tracker)
	}
}

func TestAnnotatesAnnounceList(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash +
		"&tr=http://t1.example.com/announce" +
		"&tr=udp://t2.example.com:6969"
	e := entry.New("title", uri)
	annotate(t, e)

	v, ok := e.Get("torrent_announce_list")
	if !ok {
		t.Fatal("announce_list not set")
	}
	list, ok := v.([]string)
	if !ok {
		t.Fatalf("announce_list type: got %T", v)
	}
	if len(list) != 2 {
		t.Errorf("want 2 trackers, got %d", len(list))
	}
}

func TestAnnotatesDisplayName(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash + "&dn=My+Show+S01E01"
	e := entry.New("title", uri)
	annotate(t, e)

	if v := e.GetString("title"); v == "" {
		t.Error("title should be set")
	}
}

func TestSkipsNonMagnetURL(t *testing.T) {
	e := entry.New("title", "http://example.com/file.torrent")
	annotate(t, e)

	if _, ok := e.Get("torrent_info_hash"); ok {
		t.Error("info_hash should not be set for non-magnet URL")
	}
}

func TestSkipsMalformedMagnet(t *testing.T) {
	e := entry.New("title", "magnet:?xt=urn:btih:BADSHORTEST")
	annotate(t, e)
	if _, ok := e.Get("torrent_info_hash"); ok {
		t.Error("info_hash should not be set for malformed magnet")
	}
}

func TestNoTrackersNoAnnounceField(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash
	e := entry.New("title", uri)
	annotate(t, e)

	if _, ok := e.Get("torrent_announce"); ok {
		t.Error("announce should not be set when no trackers")
	}
}

// TestAnnotateBatchSkipsNonMagnet verifies that AnnotateBatch ignores entries
// whose URL is not a magnet URI and does not block.
func TestAnnotateBatchSkipsNonMagnet(t *testing.T) {
	p := &magnetPlugin{} // no client — should not be reached for non-magnet entries
	entries := []*entry.Entry{
		entry.New("torrent", "http://example.com/file.torrent"),
		entry.New("page", "https://example.com/"),
	}
	if err := p.AnnotateBatch(context.Background(), taskCtx(), entries); err != nil {
		t.Fatalf("AnnotateBatch: %v", err)
	}
	for _, e := range entries {
		if _, ok := e.Get("torrent_info_hash"); ok {
			t.Errorf("%s: info_hash should not be set", e.URL)
		}
	}
}

// TestAnnotateBatchSetsURIFields verifies that AnnotateBatch sets URI-derived
// fields even when DHT resolution times out immediately.
func TestAnnotateBatchSetsURIFields(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash + "&tr=" + tracker + "&dn=My+Show"

	p, err := newPlugin(map[string]any{"resolve_timeout": "1ms"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	mp := p.(*magnetPlugin)

	e := entry.New("title", uri)
	if err := mp.AnnotateBatch(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("AnnotateBatch: %v", err)
	}

	if v := e.GetString("torrent_info_hash"); v != hexHash {
		t.Errorf("info_hash: got %q, want %q", v, hexHash)
	}
	if v := e.GetString("torrent_announce"); v != tracker {
		t.Errorf("announce: got %q, want %q", v, tracker)
	}
	if v := e.GetString("title"); v == "" {
		t.Error("title should be set")
	}
}

// TestAnnotateBatchMalformedMagnetSkipped verifies that a malformed magnet URI
// in a batch does not cause an error and leaves the entry unmodified.
func TestAnnotateBatchMalformedMagnetSkipped(t *testing.T) {
	p, err := newPlugin(map[string]any{"resolve_timeout": "1ms"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	mp := p.(*magnetPlugin)

	e := entry.New("title", "magnet:?xt=urn:btih:TOOSHORT")
	if err := mp.AnnotateBatch(context.Background(), taskCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("AnnotateBatch: %v", err)
	}
	if _, ok := e.Get("torrent_info_hash"); ok {
		t.Error("info_hash should not be set for malformed magnet")
	}
}

// TestNewPluginInvalidTimeout verifies that an invalid resolve_timeout returns an error.
func TestNewPluginInvalidTimeout(t *testing.T) {
	_, err := newPlugin(map[string]any{"resolve_timeout": "not-a-duration"}, nil)
	if err == nil {
		t.Error("expected error for invalid resolve_timeout")
	}
}
