package pathscrub

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makePlugin(t *testing.T, cfg map[string]any) *scrubPlugin {
	t.Helper()
	p, err := newPlugin(cfg)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*scrubPlugin)
}

func modify(t *testing.T, p *scrubPlugin, e *entry.Entry) {
	t.Helper()
	if err := p.Modify(context.Background(), &plugin.TaskContext{}, e); err != nil {
		t.Fatalf("Modify: %v", err)
	}
}

func TestWindowsColon(t *testing.T) {
	p := makePlugin(t, map[string]any{"target": "windows", "fields": "download_path"})
	e := entry.New("title", "url")
	e.Set("download_path", "House: M.D.")
	modify(t, p, e)
	v, _ := e.Get("download_path")
	// Trailing dot is also stripped on Windows.
	if v.(string) != "House_ M.D" {
		t.Errorf("got %q", v)
	}
}

func TestWindowsReservedName(t *testing.T) {
	p := makePlugin(t, map[string]any{"target": "windows"})
	e := entry.New("title", "url")
	e.Set("download_path", "CON")
	modify(t, p, e)
	v, _ := e.Get("download_path")
	if v.(string) != "CON_" {
		t.Errorf("got %q, want CON_", v)
	}
}

func TestWindowsTrailingDot(t *testing.T) {
	p := makePlugin(t, map[string]any{"target": "windows"})
	e := entry.New("title", "url")
	e.Set("download_path", "name.")
	modify(t, p, e)
	v, _ := e.Get("download_path")
	if v.(string) != "name" {
		t.Errorf("got %q, want name", v)
	}
}

func TestLinuxOnlyForbidsSlash(t *testing.T) {
	p := makePlugin(t, map[string]any{"target": "linux", "fields": "download_path"})
	e := entry.New("title", "url")
	e.Set("download_path", "file:name")
	modify(t, p, e)
	v, _ := e.Get("download_path")
	// colon is fine on Linux
	if v.(string) != "file:name" {
		t.Errorf("got %q, want file:name", v)
	}
}

func TestDefaultFieldIsDownloadPath(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	if p.fields[0] != "download_path" {
		t.Errorf("expected default field download_path, got %q", p.fields[0])
	}
}

func TestMissingFieldSkipped(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": "nonexistent"})
	e := entry.New("title", "url")
	modify(t, p, e) // should not panic
}

func TestInvalidTarget(t *testing.T) {
	_, err := newPlugin(map[string]any{"target": "macos"})
	if err == nil {
		t.Error("expected error for invalid target")
	}
}

func TestScrubExported(t *testing.T) {
	got := Scrub("file:name", "windows")
	if got != "file_name" {
		t.Errorf("got %q", got)
	}
}
