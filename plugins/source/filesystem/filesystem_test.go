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
	_, err := newFilesystemPlugin(map[string]any{}, nil)
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

	p, err := newFilesystemPlugin(map[string]any{"path": dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := p.(*filesystemPlugin).Generate(context.Background(), makeCtx(t))
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

	p, _ := newFilesystemPlugin(map[string]any{"path": dir, "mask": "*.torrent"}, nil)
	entries, err := p.(*filesystemPlugin).Generate(context.Background(), makeCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("want 2 .torrent entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.GetString("file_extension") != ".torrent" {
			t.Errorf("unexpected extension: %q", e.GetString("file_extension"))
		}
	}
}

func TestNonRecursiveSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	os.WriteFile(filepath.Join(dir, "top.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(sub, "nested.txt"), []byte("x"), 0o600)

	p, _ := newFilesystemPlugin(map[string]any{"path": dir, "recursive": false}, nil)
	entries, _ := p.(*filesystemPlugin).Generate(context.Background(), makeCtx(t))
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

	p, _ := newFilesystemPlugin(map[string]any{"path": dir, "recursive": true}, nil)
	entries, _ := p.(*filesystemPlugin).Generate(context.Background(), makeCtx(t))
	if len(entries) != 2 {
		t.Errorf("want 2 entries (recursive), got %d", len(entries))
	}
}

func TestEntryFields(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.torrent"), []byte("hello"), 0o600)

	p, _ := newFilesystemPlugin(map[string]any{"path": dir}, nil)
	entries, _ := p.(*filesystemPlugin).Generate(context.Background(), makeCtx(t))
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Title != "file.torrent" {
		t.Errorf("title: want %q, got %q", "file.torrent", e.Title)
	}
	if e.GetString("file_extension") != ".torrent" {
		t.Errorf("extension: want .torrent, got %q", e.GetString("file_extension"))
	}
	if e.GetInt("file_size") != 5 {
		t.Errorf("file_size: want 5, got %d", e.GetInt("file_size"))
	}
	if e.GetString("file_name") != "file.torrent" {
		t.Errorf("filename: want %q, got %q", "file.torrent", e.GetString("file_name"))
	}
}

func TestRegistered(t *testing.T) {
	d, ok := plugin.Lookup("filesystem")
	if !ok {
		t.Fatal("filesystem plugin not registered")
	}
	if d.Role != plugin.RoleSource {
		t.Errorf("want phase input, got %s", d.Role)
	}
}
