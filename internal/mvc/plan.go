package mvc

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Layout is how the two eyes are arranged in the output frame.
type Layout string

const (
	// LayoutFullSBS keeps both eyes at source width: 3840x1080 from a 1080p
	// disc, so 1920x1080 per eye. This is what a 3D library normally wants —
	// it is the only layout that preserves the disc's resolution.
	LayoutFullSBS Layout = "full"
	// LayoutHalfSBS squeezes both eyes into one source-width frame:
	// 1920x1080 total, so 960x1080 per eye. Half the horizontal detail, and
	// roughly half the bitrate.
	LayoutHalfSBS Layout = "half"
)

// Options configure a conversion.
type Options struct {
	Input   string // .iso, a BDMV directory, or an MKV from MakeMKV
	Output  string // destination .mkv
	TempDir string // scratch space for the demuxed streams
	Layout  Layout
	Encoder Encoder
	// Codec is the output video codec. H.264 plays on anything; HEVC is
	// materially smaller for a double-width side-by-side frame.
	Codec Codec
	// CRF is the quality target. Lower is better; 18 is visually transparent
	// for most sources.
	//
	// It is not comparable across codecs: x265 at a given CRF is roughly a
	// step higher quality — and larger — than x264 at the same number, so the
	// same value yields a better-looking HEVC file rather than a smaller one.
	// Nothing here adjusts it, because silently re-interpreting a number the
	// operator typed is worse than documenting what it means.
	CRF int
	// Preset is the encoder's speed/efficiency trade-off. x264 and x265 take
	// the same preset names.
	Preset string
	// VAAPIDevice is the render node for VAAPI encoding.
	VAAPIDevice string
	// SwapLR exchanges the eyes. Most discs put the left eye in the base view,
	// but not all, and a swapped pair is unwatchable rather than subtly wrong.
	//
	// edge264 emits base-left, so the exchange is a filter on the stacked frame
	// and therefore needs an ffmpeg encoder; Validate refuses it with a
	// software encoder.
	SwapLR bool
}

// Step is one external command in the conversion.
type Step struct {
	// Name is a short label for progress reporting.
	Name string
	// Argv is the program and its arguments, ready for exec. No shell is
	// involved, so nothing here is quoted or escaped for one.
	Argv []string
	// PipeTo, when set, names the step this one's stdout feeds. The decoder
	// streams raw frames to the encoder rather than writing an intermediate
	// file, which for a feature film is hundreds of gigabytes saved.
	PipeTo string
	// StdinIsPair marks the step whose stdin is the interleaved MVC stream,
	// written by Interleave rather than read from a file.
	StdinIsPair bool
}

// Plan is the full sequence for one conversion.
type Plan struct {
	Steps []Step
	// BaseView and DependentView are where the demux puts the two views, and
	// what the runner interleaves into the decoder's stdin.
	BaseView      string
	DependentView string
	// Intermediates are the files the steps create, for cleanup.
	Intermediates []string
}

// DefaultOptions returns sensible starting settings. Nothing here varies by
// platform — the encoder is resolved separately, by what is installed.
func DefaultOptions() Options {
	return Options{
		Layout:      LayoutFullSBS,
		Encoder:     EncoderAuto,
		Codec:       CodecH264,
		CRF:         18,
		Preset:      "slow",
		VAAPIDevice: "/dev/dri/renderD128",
	}
}

// Validate checks the options are coherent before anything is run. A
// conversion takes hours, so a configuration error must surface immediately
// rather than at the step that trips over it.
func (o Options) Validate(goos string) error {
	if o.Input == "" {
		return fmt.Errorf("no input given")
	}
	if o.Output == "" {
		return fmt.Errorf("no output given")
	}
	if strings.EqualFold(o.Input, o.Output) {
		return fmt.Errorf("input and output are the same file")
	}
	if ext := strings.ToLower(filepath.Ext(o.Output)); ext != ".mkv" {
		return fmt.Errorf("output must be a .mkv (got %q)", ext)
	}
	switch o.Layout {
	case LayoutFullSBS, LayoutHalfSBS:
	default:
		return fmt.Errorf("unknown layout %q (want %q or %q)", o.Layout, LayoutFullSBS, LayoutHalfSBS)
	}
	if !o.Codec.Valid() {
		return fmt.Errorf("unknown codec %q (want %s)", o.Codec, codecList(Codecs()))
	}
	if !SupportsEncoder(goos, o.Encoder) {
		return fmt.Errorf("encoder %q is not available on %s (have: %s)",
			o.Encoder, goos, encoderList(Encoders(goos)))
	}
	if o.CRF < 0 || o.CRF > 51 {
		return fmt.Errorf("crf %d out of range 0-51", o.CRF)
	}
	// The eye swap and the half-SBS squeeze are both filters on the stacked
	// frame, and neither x264 nor x265 has filters. Saying so here beats a run
	// that produces a file silently missing what was asked for.
	if !o.Encoder.UsesFFmpeg() && o.Encoder != EncoderAuto {
		if o.SwapLR {
			return fmt.Errorf("--swap-lr needs an ffmpeg encoder; the software encoders have no filters")
		}
		if o.Layout == LayoutHalfSBS {
			return fmt.Errorf("half-SBS needs an ffmpeg encoder to rescale; the software encoders have no filters")
		}
	}
	return nil
}

func codecList(cs []Codec) string {
	s := make([]string, len(cs))
	for i, c := range cs {
		s[i] = string(c)
	}
	return strings.Join(s, " or ")
}

func encoderList(encs []Encoder) string {
	s := make([]string, len(encs))
	for i, e := range encs {
		s[i] = string(e)
	}
	return strings.Join(s, ", ")
}

// swapFilter exchanges the two halves of a side-by-side frame: split the frame,
// crop each eye, and stack them the other way round. Verified against a real
// decode — the result's left half is bit-identical to the source's right half.
const swapFilter = "split=2[a][b];[a]crop=iw/2:ih:iw/2:0[r];[b]crop=iw/2:ih:0:0[l];[r][l]hstack=2"

// halfFilter squeezes a stacked pair back to source width, which is half-SBS.
const halfFilter = "scale=iw/2:ih"

// videoFilters is the chain the encoder applies to the stacked frames, in the
// order they must happen: swap the eyes first, then squeeze, then anything the
// encoder itself needs (VAAPI's upload). Empty when there is nothing to do.
func videoFilters(opts Options, encoderChain string) string {
	var parts []string
	if opts.SwapLR {
		parts = append(parts, swapFilter)
	}
	if opts.Layout == LayoutHalfSBS {
		parts = append(parts, halfFilter)
	}
	if encoderChain != "" {
		parts = append(parts, encoderChain)
	}
	return strings.Join(parts, ",")
}

// BuildPlan assembles the conversion for opts. It does not touch the
// filesystem or run anything — the result can be printed for a dry run and is
// what the tests assert against.
func BuildPlan(goos string, opts Options) (*Plan, error) {
	if err := opts.Validate(goos); err != nil {
		return nil, err
	}
	tmp := opts.TempDir
	if tmp == "" {
		tmp = filepath.Dir(opts.Output)
	}
	var (
		meta     = filepath.Join(tmp, "demux.meta")
		baseView = filepath.Join(tmp, "base.264")
		depView  = filepath.Join(tmp, "dependent.mvc")
		videoOut = filepath.Join(tmp, "stacked"+opts.Codec.streamExt())
	)

	p := &Plan{
		BaseView:      baseView,
		DependentView: depView,
		Intermediates: []string{meta, baseView, depView, videoOut},
	}

	// 1. Demux. tsMuxeR reads a meta file describing what to extract; writing
	//    it is the runner's job, since it depends on the source's track list.
	p.Steps = append(p.Steps, Step{
		Name: "demux",
		Argv: []string{"tsMuxeR", meta, tmp},
	})

	// 2. Decode. The two demuxed views are interleaved into the combined MVC
	//    stream on the decoder's stdin — no third copy of a multi-gigabyte pair
	//    on disk — and -O has it stack the eyes side by side as it decodes, so
	//    there is no separate stacking pass. -k keeps it going past the type-24
	//    NALs a real 3D Blu-ray carries, which a player skips too.
	//
	//    Y4M goes straight into the encoder rather than to disk: raw frames for
	//    a feature film are hundreds of gigabytes.
	p.Steps = append(p.Steps, Step{
		Name:        "decode",
		Argv:        []string{"edge264", "-y", "-k", "-O", "-"},
		StdinIsPair: true,
		PipeTo:      "encode",
	})

	// 3. Encode.
	p.Steps = append(p.Steps, encodeStep(opts, videoOut))

	// 4. Mux. Audio, subtitle and chapter arguments are appended by the runner
	//    from what the demux actually produced.
	p.Steps = append(p.Steps, Step{
		Name: "mux",
		Argv: []string{"mkvmerge", "-o", opts.Output, videoOut},
	})
	return p, nil
}

// encodeStep builds the encoder invocation. Every variant reads Y4M on stdin,
// which is what lets the decode stream straight into it. The codec changes the
// encoder name and, for software, which binary is driven; the rate-control
// flags belong to the backend and do not vary with it.
func encodeStep(opts Options, out string) Step {
	name := opts.Codec.ffmpegEncoder(opts.Encoder)
	ff := func(codecArgs []string, chain string) Step {
		argv := []string{"ffmpeg", "-hide_banner", "-y", "-f", "yuv4mpegpipe", "-i", "-"}
		if f := videoFilters(opts, chain); f != "" {
			argv = append(argv, "-vf", f)
		}
		argv = append(argv, codecArgs...)
		return Step{Name: "encode", Argv: append(argv, out)}
	}
	switch opts.Encoder {
	case EncoderVAAPI:
		// The upload has to come last: the filters before it work on software
		// frames, and once uploaded they cannot.
		return Step{Name: "encode", Argv: append([]string{
			"ffmpeg", "-hide_banner", "-y",
			"-vaapi_device", opts.VAAPIDevice,
			"-f", "yuv4mpegpipe", "-i", "-",
			"-vf", videoFilters(opts, "format=nv12,hwupload"),
			"-c:v", name, "-qp", fmt.Sprint(opts.CRF),
		}, out)}
	case EncoderVideoToolbox:
		return ff([]string{"-c:v", name, "-q:v", fmt.Sprint(opts.CRF)}, "")
	case EncoderNVENC:
		return ff([]string{"-c:v", name, "-rc", "constqp", "-qp", fmt.Sprint(opts.CRF)}, "")
	default:
		if opts.Codec == CodecH265 {
			// x265 reads stdin through --input, and needs --y4m told to it:
			// it infers the format from the file extension, which "-" has not
			// got.
			return Step{Name: "encode", Argv: []string{
				"x265", "--y4m", "--input", "-",
				"--crf", fmt.Sprint(opts.CRF),
				"--preset", opts.Preset,
				"--output", out,
			}}
		}
		return Step{Name: "encode", Argv: []string{
			"x264", "--demuxer", "y4m",
			"--crf", fmt.Sprint(opts.CRF),
			"--preset", opts.Preset,
			"--output", out, "-",
		}}
	}
}

// String renders a plan as the shell-ish transcript a dry run prints. It is
// for reading, not for execution — the steps are run directly, never a shell.
func (p *Plan) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# the two demuxed views are interleaved in-process into step 2's stdin:\n")
	fmt.Fprintf(&b, "#   %s + %s\n\n", p.BaseView, p.DependentView)
	for i, s := range p.Steps {
		fmt.Fprintf(&b, "# step %d: %s\n", i+1, s.Name)
		b.WriteString(strings.Join(s.Argv, " "))
		if s.StdinIsPair {
			b.WriteString("   < (interleaved MVC stream)")
		}
		if s.PipeTo != "" {
			fmt.Fprintf(&b, "   | (into %q)", s.PipeTo)
		}
		b.WriteString("\n")
	}
	return b.String()
}
