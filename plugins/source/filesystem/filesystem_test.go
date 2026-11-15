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

// TestEmptyDirs: with empty_dirs, every empty folder below the path is
// emitted beside the files, whatever the mask, and the scanned folder itself
// never is. A folder holding only an empty folder is not empty: removing the
// inner one empties it, and delete prunes it then.
func TestEmptyDirs(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"Film (2020)", "Show/Season 02", "Full"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Full", "a.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gen := func(cfg map[string]any) map[string]bool {
		t.Helper()
		p, err := newFilesystemPlugin(cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		es, err := p.(*filesystemPlugin).Generate(context.Background(), makeCtx(t))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, e := range es {
			rel, _ := filepath.Rel(dir, e.GetString("file_location"))
			got[rel] = true
		}
		return got
	}

	got := gen(map[string]any{"path": dir, "recursive": true, "empty_dirs": true, "mask": "*.mkv"})
	want := map[string]bool{"Film (2020)": true, filepath.Join("Show", "Season 02"): true, filepath.Join("Full", "a.mkv"): true}
	if len(got) != len(want) {
		t.Errorf("recursive: got %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("recursive: missing %s in %v", k, got)
		}
	}

	// Not recursive: only the top level is looked at.
	got = gen(map[string]any{"path": dir, "empty_dirs": true})
	if len(got) != 1 || !got["Film (2020)"] {
		t.Errorf("top level: got %v, want only Film (2020)", got)
	}

	// Off by default.
	got = gen(map[string]any{"path": dir, "recursive": true})
	if len(got) != 1 || !got[filepath.Join("Full", "a.mkv")] {
		t.Errorf("default: got %v, want only the file", got)
	}
}
