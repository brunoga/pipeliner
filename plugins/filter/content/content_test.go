package content

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makePlugin(t *testing.T, cfg map[string]any) *contentPlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*contentPlugin)
}

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func filter(t *testing.T, p *contentPlugin, e *entry.Entry) {
	t.Helper()
	if err := p.Filter(context.Background(), tc(), e); err != nil {
		t.Fatalf("Filter: %v", err)
	}
}

func entryWithFiles(files []string) *entry.Entry {
	e := entry.New("show.S01E01.720p", "http://example.com/show.torrent")
	e.Set("torrent_files", files)
	return e
}

func TestRejectMatchingFile(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar"}})
	e := entryWithFiles([]string{"show.mkv", "extras.rar"})
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("entry with .rar file should be rejected")
	}
}

func TestRejectNoMatch(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar"}})
	e := entryWithFiles([]string{"show.mkv", "subs.srt"})
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("entry without .rar file should not be rejected")
	}
}

func TestRejectMultiplePatterns(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar", "*.exe"}})
	e := entryWithFiles([]string{"setup.exe"})
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("entry with .exe should be rejected")
	}
}

func TestRejectNestedPath(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar"}})
	e := entryWithFiles([]string{"show/season1/episode.rar"})
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("nested .rar file should be rejected")
	}
}

func TestNoFilesAvailableSkipsAndDoesNotReject(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar"}})
	// Query-only URL — no useful path component, no torrent_files, no file_location.
	e := entry.New("title", "https://jackett.host/dl/torrenting/?key=abc&path=XYZ")
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("entry with no determinable file list should not be rejected")
	}
}

func TestURLFallbackRejectsMatchingFilename(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar"}})
	// Direct-download URL whose path reveals the file type — no torrent_files.
	e := entry.New("title", "http://example.com/downloads/movie.rar")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("direct .rar URL should be rejected via URL fallback")
	}
}

func TestURLFallbackDoesNotRejectNonMatching(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar"}})
	e := entry.New("title", "http://example.com/downloads/movie.mkv")
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("direct .mkv URL should not be rejected by *.rar pattern")
	}
}

func TestFileLocationFallbackRejects(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar"}})
	e := entry.New("title", "http://example.com/dl")
	e.Set(entry.FieldFileLocation, "/downloads/archive.rar")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("file_location pointing to .rar should be rejected")
	}
}

func TestMagnetURLNotUsedAsFallback(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject": []any{"*.rar"}})
	// Magnet URI — should not be parsed as a filename fallback.
	e := entry.New("title", "magnet:?xt=urn:btih:aabbccdd&dn=movie.rar")
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("magnet URI should not be used as URL filename fallback")
	}
}

func TestRequireSatisfied(t *testing.T) {
	p := makePlugin(t, map[string]any{"require": []any{"*.mkv"}})
	e := entryWithFiles([]string{"show.mkv"})
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("entry satisfying require should not be rejected")
	}
}

func TestRequireNotSatisfied(t *testing.T) {
	p := makePlugin(t, map[string]any{"require": []any{"*.mkv"}})
	e := entryWithFiles([]string{"show.avi"})
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("entry not satisfying require should be rejected")
	}
}

func TestBothRejectAndRequire(t *testing.T) {
	p := makePlugin(t, map[string]any{
		"reject":  []any{"*.rar"},
		"require": []any{"*.mkv"},
	})
	// Has .mkv but also .rar — reject wins
	e := entryWithFiles([]string{"show.mkv", "extras.rar"})
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("entry with .rar should be rejected even if .mkv present")
	}
}

func TestInvalidPattern(t *testing.T) {
	_, err := newPlugin(map[string]any{"reject": []any{"[invalid"}}, nil)
	if err == nil {
		t.Error("expected error for invalid glob pattern")
	}
}

func TestEmptyConfig(t *testing.T) {
	_, err := newPlugin(map[string]any{}, nil)
	if err == nil {
		t.Error("expected error when neither reject nor require is set")
	}
}
