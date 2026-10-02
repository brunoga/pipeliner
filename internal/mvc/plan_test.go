package mvc

import (
	"strings"
	"testing"
)

func opts(goos string, mut func(*Options)) Options {
	o := DefaultOptions()
	o.Input = "/media/in/Life of Pi (2012).iso"
	o.Output = "/media/out/Life of Pi (2012).mkv"
	o.TempDir = "/tmp/work"
	o.Encoder = EncoderSoftware
	if mut != nil {
		mut(&o)
	}
	return o
}

func TestBuildPlanHasTheFourStagesInOrder(t *testing.T) {
	p, err := BuildPlan("linux", opts("linux", nil))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"demux", "decode", "encode", "mux"}
	if len(p.Steps) != len(want) {
		t.Fatalf("got %d steps, want %d", len(p.Steps), len(want))
	}
	for i, n := range want {
		if p.Steps[i].Name != n {
			t.Errorf("step %d = %q, want %q", i, p.Steps[i].Name, n)
		}
	}
}

// The decode must stream into the encoder. Writing raw frames to disk for a
// feature film is hundreds of gigabytes.
func TestDecodeStreamsIntoTheEncoder(t *testing.T) {
	p, _ := BuildPlan("linux", opts("linux", nil))
	var decode Step
	for _, s := range p.Steps {
		if s.Name == "decode" {
			decode = s
		}
	}
	if decode.PipeTo != "encode" {
		t.Errorf("decode should pipe into encode, got %q", decode.PipeTo)
	}
	if !decode.StdinIsPair {
		t.Error("decode's stdin is the interleaved pair, written in process")
	}
	if !contains(decode.Argv, "-O") {
		t.Errorf("decode must ask for the stacked side-by-side output, got %v", decode.Argv)
	}
	if !contains(decode.Argv, "-k") {
		t.Errorf("decode must keep going past the type-24 NALs a 3D disc carries, got %v", decode.Argv)
	}
	if !contains(decode.Argv, "-") {
		t.Errorf("decode must read stdin, got %v", decode.Argv)
	}
}

// Nothing in the plan may route the two views through a file: the point of
// interleaving in process is that a multi-gigabyte pair is never copied.
func TestNoThirdCopyOfTheStreams(t *testing.T) {
	p, _ := BuildPlan("linux", opts("linux", nil))
	if p.BaseView == "" || p.DependentView == "" {
		t.Fatal("the plan must say where the demux puts the two views")
	}
	for _, s := range p.Steps {
		for _, a := range s.Argv {
			if a == "combined.264" || strings.HasSuffix(a, "interleaved.264") {
				t.Errorf("step %q writes a combined stream to disk: %v", s.Name, s.Argv)
			}
		}
	}
}

// Every encoder variant has to read the same Y4M stream on stdin, or the pipe
// from the decoder cannot work.
func TestEveryEncoderReadsY4MFromStdin(t *testing.T) {
	for _, enc := range []Encoder{EncoderSoftware, EncoderVAAPI, EncoderVideoToolbox, EncoderNVENC} {
		goos := "linux"
		if enc == EncoderVideoToolbox {
			goos = "darwin"
		}
		p, err := BuildPlan(goos, opts(goos, func(o *Options) { o.Encoder = enc }))
		if err != nil {
			t.Fatalf("%s: %v", enc, err)
		}
		var step Step
		for _, s := range p.Steps {
			if s.Name == "encode" {
				step = s
			}
		}
		joined := strings.Join(step.Argv, " ")
		if !strings.Contains(joined, "y4m") && !strings.Contains(joined, "yuv4mpegpipe") {
			t.Errorf("%s: encoder must read y4m, got %v", enc, step.Argv)
		}
		if !contains(step.Argv, "-") {
			t.Errorf("%s: encoder must read stdin, got %v", enc, step.Argv)
		}
	}
}

func TestEncoderSelectionPicksTheRightBinary(t *testing.T) {
	cases := []struct {
		goos string
		enc  Encoder
		prog string
		tag  string
	}{
		{"linux", EncoderSoftware, "x264", "--crf"},
		{"linux", EncoderVAAPI, "ffmpeg", "h264_vaapi"},
		{"linux", EncoderNVENC, "ffmpeg", "h264_nvenc"},
		{"darwin", EncoderVideoToolbox, "ffmpeg", "h264_videotoolbox"},
	}
	for _, c := range cases {
		p, err := BuildPlan(c.goos, opts(c.goos, func(o *Options) { o.Encoder = c.enc }))
		if err != nil {
			t.Fatalf("%s/%s: %v", c.goos, c.enc, err)
		}
		for _, s := range p.Steps {
			if s.Name != "encode" {
				continue
			}
			if s.Argv[0] != c.prog {
				t.Errorf("%s: program = %q, want %q", c.enc, s.Argv[0], c.prog)
			}
			if !strings.Contains(strings.Join(s.Argv, " "), c.tag) {
				t.Errorf("%s: argv should mention %q, got %v", c.enc, c.tag, s.Argv)
			}
		}
	}
}

func TestValidateRejectsIncoherentOptions(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Options)
		want string
	}{
		{"no input", func(o *Options) { o.Input = "" }, "no input"},
		{"no output", func(o *Options) { o.Output = "" }, "no output"},
		{"same file", func(o *Options) { o.Output = o.Input }, "same file"},
		{"not an mkv", func(o *Options) { o.Output = "/out/x.mp4" }, "must be a .mkv"},
		{"unknown layout", func(o *Options) { o.Layout = "sbs3d" }, "unknown layout"},
		{"crf out of range", func(o *Options) { o.CRF = 99 }, "out of range"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := BuildPlan("linux", opts("linux", c.mut))
			if err == nil {
				t.Fatalf("expected an error mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q should mention %q", err, c.want)
			}
		})
	}
}

// A GPU encoder the platform cannot have must be refused before the run, not
// hours in at the first ffmpeg call.
func TestValidateRejectsAnImpossibleEncoder(t *testing.T) {
	_, err := BuildPlan("darwin", opts("darwin", func(o *Options) { o.Encoder = EncoderVAAPI }))
	if err == nil {
		t.Fatal("VAAPI on macOS should be refused")
	}
	if !strings.Contains(err.Error(), "not available on darwin") {
		t.Errorf("error should name the platform, got %q", err)
	}
	if !strings.Contains(err.Error(), "videotoolbox") {
		t.Errorf("error should list what is available, got %q", err)
	}
}

// The output path a user gives with spaces must reach the muxer as one
// argument — the plan is argv, never a shell string.
func TestPathsWithSpacesStayOneArgument(t *testing.T) {
	p, _ := BuildPlan("linux", opts("linux", nil))
	for _, s := range p.Steps {
		if s.Name != "mux" {
			continue
		}
		if !contains(s.Argv, "/media/out/Life of Pi (2012).mkv") {
			t.Errorf("output path should be a single argv element, got %v", s.Argv)
		}
	}
}

func TestIntermediatesAreListedForCleanup(t *testing.T) {
	p, _ := BuildPlan("linux", opts("linux", nil))
	if len(p.Intermediates) == 0 {
		t.Fatal("a plan must say what it will leave behind")
	}
	for _, f := range p.Intermediates {
		if !strings.HasPrefix(f, "/tmp/work") {
			t.Errorf("intermediate %q should live in the temp dir", f)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// edge264 emits base-left and has no swap option, so exchanging the eyes is a
// filter on the stacked frame. The filter was verified against a real decode:
// the result's left half is bit-identical to the source's right half.
func TestSwapIsAFilterOnTheStackedFrame(t *testing.T) {
	p, err := BuildPlan("linux", opts("linux", func(o *Options) {
		o.Encoder = EncoderNVENC
		o.SwapLR = true
	}))
	if err != nil {
		t.Fatal(err)
	}
	joined := encodeArgs(p)
	if !strings.Contains(joined, "hstack=2") {
		t.Errorf("swap should stack the cropped halves the other way round, got: %s", joined)
	}
	if !strings.Contains(joined, "crop=iw/2:ih:iw/2:0") {
		t.Errorf("swap should crop the right eye, got: %s", joined)
	}
}

// x264 has no filters, so asking it for a swap or a squeeze has to be refused
// rather than silently producing a file missing what was asked for.
func TestFilterOptionsAreRefusedWithX264(t *testing.T) {
	if _, err := BuildPlan("linux", opts("linux", func(o *Options) { o.SwapLR = true })); err == nil {
		t.Error("--swap-lr with x264 must be refused")
	} else if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("the error should say what to use instead, got %q", err)
	}
	if _, err := BuildPlan("linux", opts("linux", func(o *Options) { o.Layout = LayoutHalfSBS })); err == nil {
		t.Error("half-SBS with x264 must be refused")
	}
}

// Order matters: the eyes are swapped before the pair is squeezed, and VAAPI's
// upload comes last because the filters before it work on software frames.
func TestFilterOrderIsSwapThenSqueezeThenUpload(t *testing.T) {
	p, err := BuildPlan("linux", opts("linux", func(o *Options) {
		o.Encoder = EncoderVAAPI
		o.SwapLR = true
		o.Layout = LayoutHalfSBS
	}))
	if err != nil {
		t.Fatal(err)
	}
	joined := encodeArgs(p)
	iSwap := strings.Index(joined, "hstack=2")
	iHalf := strings.Index(joined, "scale=iw/2:ih")
	iUp := strings.Index(joined, "hwupload")
	if iSwap < 0 || iHalf < 0 || iUp < 0 {
		t.Fatalf("all three filters should be present, got: %s", joined)
	}
	if !(iSwap < iHalf && iHalf < iUp) {
		t.Errorf("filters out of order (swap %d, squeeze %d, upload %d): %s", iSwap, iHalf, iUp, joined)
	}
}

// Full-SBS with a hardware encoder needs no resample at all: the decoder's own
// output is already the layout we want.
func TestFullSBSAddsNoScaleFilter(t *testing.T) {
	p, err := BuildPlan("linux", opts("linux", func(o *Options) { o.Encoder = EncoderNVENC }))
	if err != nil {
		t.Fatal(err)
	}
	if joined := encodeArgs(p); strings.Contains(joined, "scale=") {
		t.Errorf("full-SBS must not rescale, got: %s", joined)
	}
}

func encodeArgs(p *Plan) string {
	for _, s := range p.Steps {
		if s.Name == "encode" {
			return strings.Join(s.Argv, " ")
		}
	}
	return ""
}
