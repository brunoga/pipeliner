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
	o.Encoder = EncoderX264
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
	want := []string{"demux", "stack", "encode", "mux"}
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
	var stack Step
	for _, s := range p.Steps {
		if s.Name == "stack" {
			stack = s
		}
	}
	if stack.PipeTo != "encode" {
		t.Errorf("stack should pipe into encode, got %q", stack.PipeTo)
	}
	if !contains(stack.Argv, "-") {
		t.Errorf("vspipe should write to stdout, got %v", stack.Argv)
	}
	if !contains(stack.Argv, "y4m") {
		t.Errorf("vspipe should emit y4m, got %v", stack.Argv)
	}
}

// Every encoder variant has to read the same Y4M stream on stdin, or the pipe
// from the decoder cannot work.
func TestEveryEncoderReadsY4MFromStdin(t *testing.T) {
	for _, enc := range []Encoder{EncoderX264, EncoderVAAPI, EncoderVideoToolbox, EncoderNVENC} {
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
		{"linux", EncoderX264, "x264", "--crf"},
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

// Full-SBS keeps both eyes at source width; half squeezes them first. Getting
// this backwards silently halves the resolution of the whole library.
func TestLayoutDrivesTheScript(t *testing.T) {
	full, _ := BuildPlan("linux", opts("linux", func(o *Options) { o.Layout = LayoutFullSBS }))
	if strings.Contains(full.Script, "resize") {
		t.Errorf("full-SBS must not rescale:\n%s", full.Script)
	}
	if !strings.Contains(full.Script, "StackHorizontal") {
		t.Error("full-SBS must stack the two views")
	}
	half, _ := BuildPlan("linux", opts("linux", func(o *Options) { o.Layout = LayoutHalfSBS }))
	if !strings.Contains(half.Script, "width // 2") {
		t.Errorf("half-SBS must halve each eye's width:\n%s", half.Script)
	}
}

// The script is Python, and the paths it embeds are full of characters Python
// would otherwise interpret.
func TestScriptQuotesPathsForPython(t *testing.T) {
	p, _ := BuildPlan("windows", opts("windows", func(o *Options) {
		o.Input = `C:\media\Life of Pi (2012)\disc.iso`
	}))
	if !strings.Contains(p.Script, `r'C:\media\Life of Pi (2012)\disc.iso'`) {
		t.Errorf("a Windows path must survive into the script intact:\n%s", p.Script)
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
