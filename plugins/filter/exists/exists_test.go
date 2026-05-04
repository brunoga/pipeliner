package exists

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func setup(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestMatchingTitleRejected(t *testing.T) {
	dir := setup(t, "My.Show.S01E01.mkv")
	p, err := newPlugin(map[string]any{"path": dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("My Show S01E01", "http://x.com/a")
	p.(*existsPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("entry matching existing file should be rejected")
	}
}

func TestNonMatchingTitleAccepted(t *testing.T) {
	dir := setup(t, "My.Show.S01E01.mkv")
	p, _ := newPlugin(map[string]any{"path": dir}, nil)
	e := entry.New("My Show S01E02", "http://x.com/a")
	p.(*existsPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsRejected() {
		t.Error("entry not matching any file should not be rejected")
	}
}

func TestFilenameFieldChecked(t *testing.T) {
	dir := setup(t, "downloaded_file.mkv")
	p, _ := newPlugin(map[string]any{"path": dir}, nil)
	e := entry.New("Something Else", "http://x.com/a")
	e.Set("filename", "downloaded_file.mkv")
	p.(*existsPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("entry with matching filename field should be rejected")
	}
}

func TestNonRecursiveSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "Hidden.Show.S01E01.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, _ := newPlugin(map[string]any{"path": dir, "recursive": false}, nil)
	e := entry.New("Hidden Show S01E01", "http://x.com/a")
	p.(*existsPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsRejected() {
		t.Error("non-recursive should not look in subdirectories")
	}
}

func TestRecursiveFindsSubdirFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "Hidden.Show.S01E01.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, _ := newPlugin(map[string]any{"path": dir, "recursive": true}, nil)
	e := entry.New("Hidden Show S01E01", "http://x.com/a")
	p.(*existsPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("recursive should find files in subdirectories")
	}
}

func TestMissingPath(t *testing.T) {
	if _, err := newPlugin(map[string]any{}, nil); err == nil {
		t.Error("expected error when path missing")
	}
}

func TestNormalizeVariants(t *testing.T) {
	cases := []struct{ a, b string }{
		{"My.Show.S01E01", "My Show S01E01"},
		{"My_Show_S01E01", "My Show S01E01"},
		{"My-Show-S01E01", "My Show S01E01"},
		{"MY SHOW S01E01", "my show s01e01"},
	}
	for _, tc := range cases {
		if normalize(tc.a) != normalize(tc.b) {
			t.Errorf("normalize(%q) != normalize(%q)", tc.a, tc.b)
		}
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("exists")
	if !ok {
		t.Fatal("exists plugin not registered")
	}
	if d.PluginPhase != plugin.PhaseFilter {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}
