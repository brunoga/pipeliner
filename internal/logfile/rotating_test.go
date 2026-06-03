package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestWriteCreatesFile: a Writer that has never been opened materialises
// the file on first Write. The file ends up holding exactly what was sent.
func TestWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")

	w := &Writer{Path: path, MaxBytes: 1 << 20, MaxArchives: 3}
	defer w.Close()

	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("file = %q, want %q", got, "hello\n")
	}
}

// TestRotation: when the next write would push past MaxBytes, the base
// file is renamed to .1 and a fresh base file is started. Older archives
// shift down (.1 → .2, .2 → .3, ...).
func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")

	// MaxBytes=10 so each 6-byte write rotates after the first one.
	w := &Writer{Path: path, MaxBytes: 10, MaxArchives: 3}
	defer w.Close()

	writes := []string{"aaaaa\n", "bbbbb\n", "ccccc\n", "ddddd\n"}
	for _, s := range writes {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatalf("write %q: %v", s, err)
		}
	}

	// After four writes that each (would) overflow on the next one, we
	// should have base + .1 + .2 + .3 with each holding one write.
	check := func(suffix, want string) {
		t.Helper()
		got, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatalf("read %s: %v", path+suffix, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path+suffix, got, want)
		}
	}
	check("", "ddddd\n")
	check(".1", "ccccc\n")
	check(".2", "bbbbb\n")
	check(".3", "aaaaa\n")
}

// TestMaxArchivesDropsOldest: once we have MaxArchives archives, the next
// rotation drops the oldest one rather than letting the archive count grow.
func TestMaxArchivesDropsOldest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")

	w := &Writer{Path: path, MaxBytes: 10, MaxArchives: 2}
	defer w.Close()

	for _, s := range []string{"aaaaa\n", "bbbbb\n", "ccccc\n", "ddddd\n"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Base = newest, .1, .2 = older two. The original "aaaaa" must be gone.
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf(".3 should not exist (MaxArchives=2), got err=%v", err)
	}
	got, _ := os.ReadFile(path + ".2")
	if string(got) != "bbbbb\n" {
		t.Errorf(".2 = %q, want %q (oldest archive should be the second-oldest write, not the very first)", got, "bbbbb\n")
	}
}

// TestReopenPreservesAppend: a Writer constructed against an existing log
// file appends to it (and accounts for the existing size for rotation),
// rather than truncating. This matters across process restarts — losing
// the prior session's tail on restart would defeat the whole point.
func TestReopenPreservesAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")

	if err := os.WriteFile(path, []byte("from-previous-run\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := &Writer{Path: path, MaxBytes: 1 << 20, MaxArchives: 3}
	defer w.Close()

	if _, err := w.Write([]byte("from-this-run\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, _ := os.ReadFile(path)
	want := "from-previous-run\nfrom-this-run\n"
	if string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// TestConcurrentWrites: many goroutines hammering Write must produce a
// file whose total byte count equals the sum of write lengths, with no
// torn lines. The mutex inside Writer is the contract here.
func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeliner.log")

	w := &Writer{Path: path, MaxBytes: 1 << 20, MaxArchives: 3}
	defer w.Close()

	const (
		workers   = 8
		perWorker = 200
	)
	var wg sync.WaitGroup
	for g := range workers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			line := strings.Repeat("x", 40) + "\n"
			for range perWorker {
				if _, err := w.Write([]byte(line)); err != nil {
					t.Errorf("worker %d: %v", g, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	got, _ := os.ReadFile(path)
	wantLen := workers * perWorker * 41 // 40 chars + '\n'
	if len(got) != wantLen {
		t.Errorf("file size = %d, want %d", len(got), wantLen)
	}
	// Every line must be the canonical 40 x's. A torn write would surface
	// as a short or oversized line.
	for i, line := range strings.Split(strings.TrimRight(string(got), "\n"), "\n") {
		if line != strings.Repeat("x", 40) {
			t.Errorf("line %d = %q (len %d), want 40-x", i, line, len(line))
			break
		}
	}
}
