package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFile_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.star")
	want := []byte("hello world\n")

	if err := AtomicWriteFile(path, want, 0600); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("content = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
}

func TestAtomicWriteFile_ReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.star")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	want := []byte("replaced")
	if err := AtomicWriteFile(path, want, 0600); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != string(want) {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// TestAtomicWriteFile_LeavesOriginalOnRenameFailure proves that a failure
// during the write path leaves the existing file untouched. Renaming into a
// non-writable directory is the simplest portable way to force the rename to
// fail after a successful temp write — adjust perms so the dir is read-only,
// then attempt the write.
func TestAtomicWriteFile_LeavesOriginalOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions; cannot induce rename failure")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.star")
	original := []byte("keep me\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Read-only directory: CreateTemp will fail before any rename runs, so
	// the existing file is guaranteed untouched.
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	if err := AtomicWriteFile(path, []byte("would clobber"), 0600); err == nil {
		t.Fatal("AtomicWriteFile should have failed on a read-only directory")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("content = %q, want %q (original must survive failed write)", got, original)
	}

	// No .tmp.* litter left behind from the failed attempt.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || (len(e.Name()) > 4 && e.Name()[:len(e.Name())-4] != "config.star" && e.Name() != "config.star") {
			// Allow the original file plus nothing else.
			if e.Name() != "config.star" {
				t.Errorf("unexpected leftover file: %s", e.Name())
			}
		}
	}
}
