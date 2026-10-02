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

// --dry-run must work on a machine where nothing is installed yet: its whole
// point is showing what would run. The encoder is pinned so the assertion does
// not depend on what hardware the test machine happens to have.
func TestDryRunNeedsNoTools(t *testing.T) {
	out, _, code := capture(t, "--dry-run", "--encoder", "x264",
		"--input", "/media/Life of Pi (2012)/disc.iso",
		"--output", "/out/Life of Pi (2012).mkv",
		"--temp", "/tmp/w")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"tsMuxeR", "edge264", "x264", "mkvmerge", "interleaved"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run should mention %q, got:\n%s", want, out)
		}
	}
	// The decoder stacks the eyes itself, so nothing else should.
	for _, unwanted := range []string{"StackHorizontal", "vspipe", "vapoursynth"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the pipeline no longer uses %q:\n%s", unwanted, out)
		}
	}
}

// Full-SBS is what the decoder emits, so it needs no filter; half-SBS squeezes
// the stacked pair, which is an ffmpeg filter and so refused with x264.
func TestDryRunShowsTheChosenLayout(t *testing.T) {
	full, _, code := capture(t, "--dry-run", "--encoder", "x264", "--layout", "full",
		"--input", "/in/a.iso", "--output", "/out/a.mkv", "--temp", "/tmp/w")
	if code != 0 {
		t.Fatalf("full-SBS with x264 should work, exit = %d", code)
	}
	if strings.Contains(full, "scale=") {
		t.Errorf("full-SBS must not rescale:\n%s", full)
	}
	if _, _, code := capture(t, "--dry-run", "--encoder", "x264", "--layout", "half",
		"--input", "/in/a.iso", "--output", "/out/a.mkv", "--temp", "/tmp/w"); code != 2 {
		t.Errorf("half-SBS with x264 should be refused, exit = %d", code)
	}
	half, _, code := capture(t, "--dry-run", "--encoder", "nvenc", "--layout", "half",
		"--input", "/in/a.iso", "--output", "/out/a.mkv", "--temp", "/tmp/w")
	if code != 0 {
		t.Fatalf("half-SBS with an ffmpeg encoder should work, exit = %d", code)
	}
	if !strings.Contains(half, "scale=iw/2:ih") {
		t.Errorf("half-SBS must squeeze the stacked pair:\n%s", half)
	}
}

// The eye swap is also a filter, so it follows the same rule.
func TestSwapRequiresAnFFmpegEncoder(t *testing.T) {
	if _, _, code := capture(t, "--dry-run", "--encoder", "x264", "--swap-lr",
		"--input", "/in/a.iso", "--output", "/out/a.mkv", "--temp", "/tmp/w"); code != 2 {
		t.Errorf("--swap-lr with x264 should be refused, exit = %d", code)
	}
	out, _, code := capture(t, "--dry-run", "--encoder", "nvenc", "--swap-lr",
		"--input", "/in/a.iso", "--output", "/out/a.mkv", "--temp", "/tmp/w")
	if code != 0 {
		t.Fatalf("--swap-lr with nvenc should work, exit = %d", code)
	}
	if !strings.Contains(out, "hstack=2") {
		t.Errorf("the swap should stack the cropped halves the other way:\n%s", out)
	}
}

// Bad configuration exits 2 with usage, not a panic or a partial run.
func TestInvalidOptionsExitTwo(t *testing.T) {
	cases := [][]string{
		{"--dry-run", "--encoder", "x264", "--output", "/out/a.mkv"},                         // no input
		{"--dry-run", "--encoder", "x264", "--input", "/in/a.iso"},                           // no output
		{"--dry-run", "--encoder", "x264", "--input", "/in/a.iso", "--output", "/out/a.mp4"}, // not mkv
		{"--dry-run", "--encoder", "x264", "--input", "/in/a.iso", "--output", "/out/a.mkv", "--crf", "99"},
		{"--dry-run", "--encoder", "x264", "--input", "/in/a.iso", "--output", "/out/a.mkv", "--layout", "sbs"},
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
