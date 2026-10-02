package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
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

// --- stable_for ---

// touch writes a file and sets its mtime to age ago.
func touch(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func generate(t *testing.T, cfg map[string]any) []*entry.Entry {
	t.Helper()
	p, err := newFilesystemPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("build plugin: %v", err)
	}
	got, err := p.(*filesystemPlugin).Generate(context.Background(), &plugin.TaskContext{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return got
}

func names(entries []*entry.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Title)
	}
	sort.Strings(out)
	return out
}

// A file still being written keeps its mtime fresh, so it must not be handed
// downstream until it has held still — otherwise a converter reads a partial
// file and, if that still parses, records a corrupt result as done.
func TestStableForSkipsRecentlyModifiedFiles(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "settled.iso"), 10*time.Minute)
	touch(t, filepath.Join(dir, "arriving.iso"), 5*time.Second)

	got := names(generate(t, map[string]any{"path": dir, "stable_for": "2m"}))
	if len(got) != 1 || got[0] != "settled.iso" {
		t.Errorf("emitted %v, want only settled.iso", got)
	}
}

// Without stable_for the plugin behaves exactly as it always has, so existing
// configs are unaffected.
func TestWithoutStableForEverythingIsEmitted(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "settled.iso"), 10*time.Minute)
	touch(t, filepath.Join(dir, "arriving.iso"), 5*time.Second)

	got := names(generate(t, map[string]any{"path": dir}))
	if len(got) != 2 {
		t.Errorf("emitted %v, want both files", got)
	}
}

// A file exactly at the boundary is settled: the test is "modified more
// recently than the window", not "at least the window old plus a tick".
func TestStableForBoundaryIsInclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edge.iso")
	touch(t, path, 0)
	// Read the stored mtime back rather than assuming the filesystem kept
	// what was written: granularity varies, and a rounded-up timestamp would
	// otherwise make this assert the opposite of what it means to.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	orig := Now
	t.Cleanup(func() { Now = orig })
	Now = func() time.Time { return st.ModTime().Add(2 * time.Minute) }

	if got := names(generate(t, map[string]any{"path": dir, "stable_for": "2m"})); len(got) != 1 {
		t.Errorf("emitted %v, want the file at exactly the window to be settled", got)
	}
}

// The window is measured from one clock reading, so a scan slow enough to
// cross the boundary cannot accept one file and reject an identically-aged
// one.
func TestStableForUsesOneClockReadingPerScan(t *testing.T) {
	dir := t.TempDir()
	fixed := time.Now()
	for _, n := range []string{"a.iso", "b.iso", "c.iso"} {
		p := filepath.Join(dir, n)
		touch(t, p, 0)
		if err := os.Chtimes(p, fixed, fixed); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	if st, err := os.Stat(filepath.Join(dir, "a.iso")); err == nil {
		fixed = st.ModTime()
	}
	orig := Now
	t.Cleanup(func() { Now = orig })
	calls := 0
	Now = func() time.Time {
		calls++
		// A clock that advances past the boundary between files would split
		// the three if it were read per file.
		return fixed.Add(2*time.Minute - time.Second + time.Duration(calls)*time.Second)
	}

	got := names(generate(t, map[string]any{"path": dir, "stable_for": "2m"}))
	if len(got) != 0 && len(got) != 3 {
		t.Errorf("emitted %v — the three files are the same age and must be decided alike", got)
	}
	if calls != 1 {
		t.Errorf("clock read %d times, want once per scan", calls)
	}
}

func TestStableForRejectsBadDurations(t *testing.T) {
	for _, bad := range []string{"soon", "2", "-5m"} {
		if _, err := newFilesystemPlugin(map[string]any{"path": t.TempDir(), "stable_for": bad}, nil); err == nil {
			t.Errorf("stable_for %q should be rejected", bad)
		}
	}
	if errs := validate(map[string]any{"path": "/x", "stable_for": "nope"}); len(errs) == 0 {
		t.Error("validate should reject a malformed stable_for")
	}
	if errs := validate(map[string]any{"path": "/x", "stable_for": "2m"}); len(errs) != 0 {
		t.Errorf("validate rejected a good stable_for: %v", errs)
	}
}
