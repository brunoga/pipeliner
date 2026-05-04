package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx(t *testing.T) *plugin.TaskContext {
	t.Helper()
	return &plugin.TaskContext{Name: "test"}
}

func TestRequiresPath(t *testing.T) {
	_, err := newFilesystemPlugin(map[string]any{})
	if err == nil {
		t.Error("expected error when path is missing")
	}
}

func TestScansDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.torrent"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	p, err := newFilesystemPlugin(map[string]any{"path": dir})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := p.(*filesystemPlugin).Run(context.Background(), makeCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("want 3 entries, got %d", len(entries))
	}
}

func TestGlobMask(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.torrent", "c.torrent"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
	}

	p, _ := newFilesystemPlugin(map[string]any{"path": dir, "mask": "*.torrent"})
	entries, err := p.(*filesystemPlugin).Run(context.Background(), makeCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("want 2 .torrent entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.GetString("extension") != ".torrent" {
			t.Errorf("unexpected extension: %q", e.GetString("extension"))
		}
	}
}

func TestNonRecursiveSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	os.WriteFile(filepath.Join(dir, "top.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(sub, "nested.txt"), []byte("x"), 0o600)

	p, _ := newFilesystemPlugin(map[string]any{"path": dir, "recursive": false})
	entries, _ := p.(*filesystemPlugin).Run(context.Background(), makeCtx(t))
	if len(entries) != 1 {
		t.Errorf("want 1 entry (non-recursive), got %d", len(entries))
	}
}

func TestRecursiveIncludesSubdirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	os.WriteFile(filepath.Join(dir, "top.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(sub, "nested.txt"), []byte("x"), 0o600)

	p, _ := newFilesystemPlugin(map[string]any{"path": dir, "recursive": true})
	entries, _ := p.(*filesystemPlugin).Run(context.Background(), makeCtx(t))
	if len(entries) != 2 {
		t.Errorf("want 2 entries (recursive), got %d", len(entries))
	}
}

func TestEntryFields(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.torrent"), []byte("hello"), 0o600)

	p, _ := newFilesystemPlugin(map[string]any{"path": dir})
	entries, _ := p.(*filesystemPlugin).Run(context.Background(), makeCtx(t))
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Title != "file.torrent" {
		t.Errorf("title: want %q, got %q", "file.torrent", e.Title)
	}
	if e.GetString("extension") != ".torrent" {
		t.Errorf("extension: want .torrent, got %q", e.GetString("extension"))
	}
	if e.GetInt("size") != 5 {
		t.Errorf("size: want 5, got %d", e.GetInt("size"))
	}
	if e.GetString("filename") != "file.torrent" {
		t.Errorf("filename: want %q, got %q", "file.torrent", e.GetString("filename"))
	}
}

func TestRegistered(t *testing.T) {
	d, ok := plugin.Lookup("filesystem")
	if !ok {
		t.Fatal("filesystem plugin not registered")
	}
	if d.PluginPhase != plugin.PhaseInput {
		t.Errorf("want phase input, got %s", d.PluginPhase)
	}
}
