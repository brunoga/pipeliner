package mvc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeLookPath makes the resolver answer from a set, so the tests run
// identically on Linux, macOS and Windows without creating executables.
func fakeLookPath(t *testing.T, present ...string) {
	t.Helper()
	set := make(map[string]bool, len(present))
	for _, p := range present {
		set[p] = true
	}
	orig := LookPath
	LookPath = func(name string) (string, error) {
		if set[name] {
			return "/fake/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { LookPath = orig })
}

func TestRequiredToolsDependOnTheEncoder(t *testing.T) {
	sw := Required("linux", EncoderX264)
	if !hasTool(sw, "x264") || hasTool(sw, "ffmpeg") {
		t.Errorf("software encoding needs x264 and not ffmpeg, got %v", names(sw))
	}
	hw := Required("linux", EncoderVAAPI)
	if !hasTool(hw, "ffmpeg") || hasTool(hw, "x264") {
		t.Errorf("hardware encoding needs ffmpeg and not x264, got %v", names(hw))
	}
	// The demux, decode and mux stages are the same either way.
	for _, want := range []string{"tsmuxer", "vspipe", "mkvmerge"} {
		if !hasTool(sw, want) || !hasTool(hw, want) {
			t.Errorf("%s is required regardless of encoder", want)
		}
	}
}

func TestDetectReportsWhatIsMissing(t *testing.T) {
	fakeLookPath(t, "ffprobe", "mkvmerge")
	rep := Detect(context.Background(), "linux", EncoderX264)
	if rep.OK() {
		t.Fatal("report should not be OK when tools are absent")
	}
	missing := names(toolsOf(rep.Missing()))
	for _, want := range []string{"tsMuxeR", "vspipe", "x264"} {
		if !containsFold(missing, want) {
			t.Errorf("missing list %v should contain %s", missing, want)
		}
	}
	for _, unwanted := range []string{"ffprobe", "mkvmerge"} {
		if containsFold(missing, unwanted) {
			t.Errorf("%s was present and must not be reported missing", unwanted)
		}
	}
}

func TestDetectIsOKWhenEverythingIsPresent(t *testing.T) {
	fakeLookPath(t, "ffprobe", "tsMuxeR", "vspipe", "x264", "mkvmerge")
	rep := Detect(context.Background(), "linux", EncoderX264)
	if !rep.OK() {
		t.Errorf("expected OK, missing: %v", names(toolsOf(rep.Missing())))
	}
}

// tsMuxeR ships under two spellings; either satisfies the requirement.
func TestDetectAcceptsEitherBinaryName(t *testing.T) {
	for _, spelling := range []string{"tsMuxeR", "tsmuxer"} {
		fakeLookPath(t, "ffprobe", spelling, "vspipe", "x264", "mkvmerge")
		if rep := Detect(context.Background(), "linux", EncoderX264); !rep.OK() {
			t.Errorf("%s should satisfy the tsmuxer requirement", spelling)
		}
	}
}

// A missing-tool report has to explain itself: which tool, what it is for,
// and where to start looking. Someone reads this once, having never seen the
// pipeline.
func TestMissingToolReportExplainsItself(t *testing.T) {
	fakeLookPath(t)
	out := Detect(context.Background(), "linux", EncoderX264).String()
	if !strings.Contains(out, "MISSING") {
		t.Error("report should mark missing tools")
	}
	if !strings.Contains(out, "demux the base and dependent MVC views") {
		t.Error("report should say what each missing tool is for")
	}
	if !strings.Contains(out, "try:") {
		t.Error("report should offer an install hint")
	}
	if !strings.Contains(out, "5 of 5 tools missing") {
		t.Errorf("report should total the misses, got:\n%s", out)
	}
}

func TestInstallHintFallsBackForAnUnlistedPlatform(t *testing.T) {
	if h := toolMkvmerge.InstallHint("plan9"); h == "" {
		t.Error("an unlisted platform should still get a starting point")
	}
	if h := toolMkvmerge.InstallHint("darwin"); !strings.Contains(h, "brew") {
		t.Errorf("darwin hint should be the macOS one, got %q", h)
	}
}

// Asking for a GPU encoder the platform cannot have is a configuration error,
// and a conversion runs for hours — so it must be caught up front.
func TestSupportsEncoderIsPlatformAware(t *testing.T) {
	cases := []struct {
		goos string
		enc  Encoder
		want bool
	}{
		{"linux", EncoderVAAPI, true},
		{"darwin", EncoderVAAPI, false},
		{"darwin", EncoderVideoToolbox, true},
		{"linux", EncoderVideoToolbox, false},
		{"windows", EncoderNVENC, true},
		{"darwin", EncoderNVENC, false},
		{"linux", EncoderX264, true},
		{"windows", EncoderX264, true},
		{"darwin", EncoderX264, true},
		{"plan9", EncoderX264, true},
		{"linux", EncoderAuto, true},
	}
	for _, c := range cases {
		if got := SupportsEncoder(c.goos, c.enc); got != c.want {
			t.Errorf("SupportsEncoder(%q, %q) = %v, want %v", c.goos, c.enc, got, c.want)
		}
	}
}

func TestDefaultEncoderFallsBackToSoftware(t *testing.T) {
	// Only the software chain is installed, so auto must not pick a GPU path.
	fakeLookPath(t, "ffprobe", "tsMuxeR", "vspipe", "x264", "mkvmerge")
	if got := DefaultEncoder(context.Background(), "linux"); got != EncoderX264 {
		t.Errorf("got %q, want x264 when no ffmpeg is installed", got)
	}
}

func TestDefaultEncoderPrefersHardwareWhenAvailable(t *testing.T) {
	fakeLookPath(t, "ffprobe", "tsMuxeR", "vspipe", "ffmpeg", "mkvmerge")
	got := DefaultEncoder(context.Background(), "darwin")
	if got != EncoderVideoToolbox {
		t.Errorf("got %q, want videotoolbox on macOS when ffmpeg is present", got)
	}
}

func TestEveryPlatformHasASoftwareFallback(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows", "plan9"} {
		encs := Encoders(goos)
		if len(encs) == 0 || encs[len(encs)-1] != EncoderX264 {
			t.Errorf("%s: x264 must be the last resort, got %v", goos, encs)
		}
	}
}

func hasTool(ts []Tool, name string) bool {
	for _, t := range ts {
		if t.Name == name {
			return true
		}
	}
	return false
}
func names(ts []Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}
func toolsOf(fs []Found) []Tool {
	out := make([]Tool, len(fs))
	for i, f := range fs {
		out[i] = f.Tool
	}
	return out
}
func containsFold(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}
