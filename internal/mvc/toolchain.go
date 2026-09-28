// Package mvc drives the conversion of a frame-packed Blu-ray 3D source (MVC)
// into a side-by-side MKV that an ordinary decoder can play.
//
// MVC stores the second eye as a dependent view of an AVC base view. Players
// that decode it are rare — Plex does not — so a 3D Blu-ray is unwatchable in
// most libraries even though it carries a full 1080p image per eye. Converting
// it to side-by-side keeps both eyes at full resolution in a single frame that
// any H.264/HEVC decoder handles.
//
// The work itself is done by external programs; this package decides which
// ones a platform needs, finds them, and assembles the commands. It follows
// the pipeline established by bd3d2sbs
// (https://github.com/Michal-Szczepaniak/bd3d2sbs), itself a Linux port of the
// Windows tool BD3D2MK3D:
//
//	tsMuxeR    demux the base (AVC) and dependent (MVC) views, plus audio,
//	           subtitles and chapters
//	VapourSynth decode both views and stack them into one frame
//	encoder    x264, or ffmpeg with a platform hardware encoder
//	mkvmerge   mux the result back together
//
// Installing those programs is left to the operator. What this package
// guarantees is that it will say precisely which are missing, and why each is
// needed, rather than failing partway through a multi-hour run.
package mvc

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Tool is an external program the conversion depends on.
type Tool struct {
	// Name is how this package and its messages refer to the tool.
	Name string
	// Binaries are candidate executable names in preference order. Windows
	// builds are found through PATHEXT, so the bare name is enough there too.
	Binaries []string
	// Purpose says what the tool does in the pipeline, so a missing-tool
	// report explains itself without the reader consulting the source.
	Purpose string
	// VersionArgs prints a version. Many of these tools exit non-zero when
	// asked, so output is used whatever the exit status.
	VersionArgs []string
	// Install maps GOOS to a hint. A hint is a starting point, not a
	// guarantee: several of these have no distribution package anywhere.
	Install map[string]string
}

// Encoder selects how the stacked frames are compressed.
type Encoder string

const (
	// EncoderAuto picks the best available for the platform.
	EncoderAuto Encoder = "auto"
	// EncoderX264 is software H.264. Available everywhere, slowest.
	EncoderX264 Encoder = "x264"
	// EncoderVAAPI is ffmpeg VAAPI — Linux only, needs a supported GPU.
	EncoderVAAPI Encoder = "vaapi"
	// EncoderVideoToolbox is ffmpeg VideoToolbox — macOS only.
	EncoderVideoToolbox Encoder = "videotoolbox"
	// EncoderNVENC is ffmpeg NVENC — NVIDIA, Linux and Windows.
	EncoderNVENC Encoder = "nvenc"
)

// Encoders returns the encoders that make sense on this platform, best first.
// The list is what the platform *supports*, not what is installed; Detect
// reports availability.
func Encoders(goos string) []Encoder {
	switch goos {
	case "linux":
		return []Encoder{EncoderNVENC, EncoderVAAPI, EncoderX264}
	case "darwin":
		return []Encoder{EncoderVideoToolbox, EncoderX264}
	case "windows":
		return []Encoder{EncoderNVENC, EncoderX264}
	default:
		return []Encoder{EncoderX264}
	}
}

// UsesFFmpeg reports whether an encoder is driven through ffmpeg rather than
// the standalone x264 binary.
func (e Encoder) UsesFFmpeg() bool { return e != EncoderX264 && e != EncoderAuto }

var (
	toolTsMuxeR = Tool{
		Name:        "tsmuxer",
		Binaries:    []string{"tsMuxeR", "tsmuxer"},
		Purpose:     "demux the base and dependent MVC views, audio, subtitles and chapters",
		VersionArgs: nil, // prints a banner with no arguments
		Install: map[string]string{
			"linux":   "build the fork bd3d2sbs vendors — distribution tsMuxeR packages usually lack MVC demuxing",
			"darwin":  "build the same fork from source; Homebrew's tsmuxer is not MVC-capable",
			"windows": "https://github.com/justdan96/tsMuxer releases",
		},
	}
	toolVSPipe = Tool{
		Name:        "vspipe",
		Binaries:    []string{"vspipe"},
		Purpose:     "run the VapourSynth script that decodes both views and stacks them",
		VersionArgs: []string{"--version"},
		Install: map[string]string{
			"linux":   "install VapourSynth (R65+) plus the mvc-source plugin and the edge264-mvc decoder",
			"darwin":  "brew install vapoursynth, then build mvc-source and edge264-mvc",
			"windows": "VapourSynth installer from vapoursynth.com, then place mvc-source in the plugins directory",
		},
	}
	toolMkvmerge = Tool{
		Name:        "mkvmerge",
		Binaries:    []string{"mkvmerge"},
		Purpose:     "mux the encoded video with audio, subtitles and chapters",
		VersionArgs: []string{"--version"},
		Install: map[string]string{
			"linux":   "install mkvtoolnix",
			"darwin":  "brew install mkvtoolnix",
			"windows": "https://mkvtoolnix.download/",
		},
	}
	toolX264 = Tool{
		Name:        "x264",
		Binaries:    []string{"x264"},
		Purpose:     "encode the stacked frames in software",
		VersionArgs: []string{"--version"},
		Install: map[string]string{
			"linux":   "install x264",
			"darwin":  "brew install x264",
			"windows": "https://www.videolan.org/developers/x264.html",
		},
	}
	toolFFmpeg = Tool{
		Name:        "ffmpeg",
		Binaries:    []string{"ffmpeg"},
		Purpose:     "encode the stacked frames with a hardware encoder",
		VersionArgs: []string{"-version"},
		Install: map[string]string{
			"linux":   "install ffmpeg",
			"darwin":  "brew install ffmpeg",
			"windows": "https://ffmpeg.org/download.html",
		},
	}
	toolFFprobe = Tool{
		Name:        "ffprobe",
		Binaries:    []string{"ffprobe"},
		Purpose:     "inspect the source's streams and runtime",
		VersionArgs: []string{"-version"},
		Install:     toolFFmpeg.Install,
	}
)

// Required returns the tools needed on goos for the given encoder, in the
// order a reader would meet them in the pipeline.
func Required(goos string, enc Encoder) []Tool {
	tools := []Tool{toolFFprobe, toolTsMuxeR, toolVSPipe}
	if enc.UsesFFmpeg() {
		tools = append(tools, toolFFmpeg)
	} else {
		tools = append(tools, toolX264)
	}
	return append(tools, toolMkvmerge)
}

// Found is the outcome of looking for one tool.
type Found struct {
	Tool
	// Path is the resolved executable, empty when the tool was not found.
	Path string
	// Version is the first line the tool printed when asked, best-effort.
	Version string
}

// OK reports whether the tool was located.
func (f Found) OK() bool { return f.Path != "" }

// Report is the result of a toolchain check.
type Report struct {
	GOOS    string
	Encoder Encoder
	Tools   []Found
}

// Missing returns the tools that could not be found.
func (r Report) Missing() []Found {
	var out []Found
	for _, f := range r.Tools {
		if !f.OK() {
			out = append(out, f)
		}
	}
	return out
}

// OK reports whether every required tool was found.
func (r Report) OK() bool { return len(r.Missing()) == 0 }

// LookPath is the executable resolver. It is a variable so tests can supply
// their own without creating real executables, which would not be portable
// across the platforms this tool targets.
var LookPath = exec.LookPath

// Detect locates every tool required on this platform for enc.
//
// A version string is best-effort: several of these programs exit non-zero
// when asked for one, and tsMuxeR has no version flag at all, so a tool that
// was found but would not report a version is still reported as present.
func Detect(ctx context.Context, goos string, enc Encoder) Report {
	rep := Report{GOOS: goos, Encoder: enc}
	for _, t := range Required(goos, enc) {
		f := Found{Tool: t}
		for _, bin := range t.Binaries {
			if p, err := LookPath(bin); err == nil {
				f.Path = p
				break
			}
		}
		if f.OK() && t.VersionArgs != nil {
			f.Version = probeVersion(ctx, f.Path, t.VersionArgs)
		}
		rep.Tools = append(rep.Tools, f)
	}
	return rep
}

// probeVersion returns the first non-empty line the tool printed, or "".
func probeVersion(ctx context.Context, path string, args []string) string {
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // path came from LookPath
	out, _ := cmd.CombinedOutput()                 // exit status is not meaningful here
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// InstallHint returns the hint for goos, falling back to any other platform's
// so a reader on an unlisted OS still gets a starting point.
func (t Tool) InstallHint(goos string) string {
	if h, ok := t.Install[goos]; ok {
		return h
	}
	if h, ok := t.Install["linux"]; ok {
		return h
	}
	return ""
}

// String renders a report as the operator-facing check output.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "platform: %s   encoder: %s\n\n", r.GOOS, r.Encoder)
	for _, f := range r.Tools {
		switch {
		case f.OK() && f.Version != "":
			fmt.Fprintf(&b, "  ok       %-10s %s\n           %s\n", f.Name, f.Path, f.Version)
		case f.OK():
			fmt.Fprintf(&b, "  ok       %-10s %s\n", f.Name, f.Path)
		default:
			fmt.Fprintf(&b, "  MISSING  %-10s %s\n", f.Name, f.Purpose)
			if h := f.InstallHint(r.GOOS); h != "" {
				fmt.Fprintf(&b, "           try: %s\n", h)
			}
		}
	}
	if r.OK() {
		fmt.Fprintf(&b, "\nall %d required tools present\n", len(r.Tools))
	} else {
		fmt.Fprintf(&b, "\n%d of %d tools missing\n", len(r.Missing()), len(r.Tools))
	}
	return b.String()
}

// DefaultEncoder resolves EncoderAuto to the best encoder actually installed
// on goos, falling back to x264, which is always the software option.
func DefaultEncoder(ctx context.Context, goos string) Encoder {
	for _, enc := range Encoders(goos) {
		if enc == EncoderX264 {
			continue
		}
		if Detect(ctx, goos, enc).OK() {
			return enc
		}
	}
	return EncoderX264
}

// SupportsEncoder reports whether enc is meaningful on goos. VAAPI on macOS
// and VideoToolbox on Linux are configuration errors worth catching early
// rather than at the first ffmpeg invocation, hours into a run.
func SupportsEncoder(goos string, enc Encoder) bool {
	if enc == EncoderAuto {
		return true
	}
	for _, e := range Encoders(goos) {
		if e == enc {
			return true
		}
	}
	return false
}
