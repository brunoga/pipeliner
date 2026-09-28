package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// capture runs the command with argv, returning stdout, stderr and the code.
// Real *os.File handles are used because that is what run takes, and they are
// what the flag package writes to.
func capture(t *testing.T, argv ...string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	mk := func(name string) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	out, errf := mk("stdout"), mk("stderr")
	code := run(argv, out, errf)
	_ = out.Close()
	_ = errf.Close()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return read("stdout"), read("stderr"), code
}

func TestVersion(t *testing.T) {
	out, _, code := capture(t, "--version")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "bd3d2sbs") {
		t.Errorf("stdout = %q, should name the tool", out)
	}
}

// --dry-run must not need the toolchain: its whole point is showing what would
// run on a machine where nothing is installed yet.
func TestDryRunNeedsNoTools(t *testing.T) {
	out, _, code := capture(t, "--dry-run",
		"--input", "/media/Life of Pi (2012)/disc.iso",
		"--output", "/out/Life of Pi (2012).mkv",
		"--temp", "/tmp/w")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"tsMuxeR", "vspipe", "x264", "mkvmerge", "StackHorizontal"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run should mention %q, got:\n%s", want, out)
		}
	}
}

func TestDryRunShowsTheChosenLayout(t *testing.T) {
	full, _, _ := capture(t, "--dry-run", "--layout", "full",
		"--input", "/in/a.iso", "--output", "/out/a.mkv", "--temp", "/tmp/w")
	if strings.Contains(full, "width // 2") {
		t.Error("full-SBS must not rescale")
	}
	half, _, _ := capture(t, "--dry-run", "--layout", "half",
		"--input", "/in/a.iso", "--output", "/out/a.mkv", "--temp", "/tmp/w")
	if !strings.Contains(half, "width // 2") {
		t.Error("half-SBS must halve each eye")
	}
}

// Bad configuration exits 2 with usage, not a panic or a partial run.
func TestInvalidOptionsExitTwo(t *testing.T) {
	cases := [][]string{
		{"--dry-run", "--output", "/out/a.mkv"},                         // no input
		{"--dry-run", "--input", "/in/a.iso"},                           // no output
		{"--dry-run", "--input", "/in/a.iso", "--output", "/out/a.mp4"}, // not mkv
		{"--dry-run", "--input", "/in/a.iso", "--output", "/out/a.mkv", "--crf", "99"},
		{"--dry-run", "--input", "/in/a.iso", "--output", "/out/a.mkv", "--layout", "sbs"},
		{"--encoder", "nonsense"},
	}
	for _, argv := range cases {
		if _, _, code := capture(t, argv...); code != 2 {
			t.Errorf("%v: exit = %d, want 2", argv, code)
		}
	}
}

// --check is a preflight, so a missing tool must be a non-zero exit a script
// can branch on, and the output must name what is missing.
func TestCheckReportsAndExitsNonZeroWhenIncomplete(t *testing.T) {
	out, _, code := capture(t, "--check")
	if !strings.Contains(out, "platform:") {
		t.Errorf("check should report the platform, got:\n%s", out)
	}
	if strings.Contains(out, "MISSING") && code == 0 {
		t.Error("exit must be non-zero while tools are missing")
	}
	if !strings.Contains(out, "MISSING") && code != 0 {
		t.Error("exit must be zero when every tool is present")
	}
}
