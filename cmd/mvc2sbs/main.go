// Command mvc2sbs converts a frame-packed Blu-ray 3D source (MVC) into a
// side-by-side MKV that an ordinary decoder can play.
//
// MVC keeps the second eye as a dependent view of an AVC base view. Very few
// players decode it — Plex does not — so a 3D Blu-ray sits in a library
// unwatchable despite carrying a full image per eye. Side-by-side puts both
// eyes in one frame that any H.264 decoder handles.
//
// The conversion is done by external programs, and installing them is left to
// the operator. What this command promises is that `--check` will say exactly
// which are missing and what each is for, rather than failing partway through
// a run that takes hours.
//
// Usage:
//
//	mvc2sbs --check
//	mvc2sbs --dry-run --input 00800.m2ts --output "Life of Pi (2012).mkv"
//	mvc2sbs --input BDMV/PLAYLIST/00800.mpls --output "Life of Pi (2012).mkv"
//
// The source is a .iso disc image, a BDMV directory (or its parent), or
// anything tsMuxeR reads directly: an .m2ts, a .mpls playlist, an MKV, or a
// VOB/MP4. An image and a directory are handled here rather than by tsMuxeR —
// the image is read in place, with no mounting and no root, and in both cases
// the feature playlist is chosen by probing.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/brunoga/pipeliner/internal/mvc"
	pversion "github.com/brunoga/pipeliner/internal/version"
)

// version is overridden at build time with
// -ldflags="-X main.version=...". Left alone, version.Resolve falls back to
// the module version the Go toolchain embeds, so a `go install` of a tagged
// version reports that tag rather than the placeholder.
var version = pversion.Placeholder

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(argv []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("mvc2sbs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		check    = fs.Bool("check", false, "report which external tools are present and which are missing, then exit")
		dryRun   = fs.Bool("dry-run", false, "print the commands that would run, without running them")
		input    = fs.String("input", "", "source: a .iso disc image, a BDMV directory, an .m2ts, a .mpls playlist, or an MKV")
		output   = fs.String("output", "", "destination .mkv")
		tempDir  = fs.String("temp", "", "scratch directory for demuxed streams (default: alongside the output)")
		layout   = fs.String("layout", string(mvc.LayoutFullSBS), "full (1080p per eye) or half (960p per eye, ~half the size)")
		encoder  = fs.String("encoder", string(mvc.EncoderAuto), "auto, software, vaapi, videotoolbox or nvenc")
		codec    = fs.String("codec", string(mvc.CodecH264), "output video codec: h264 (plays anywhere) or h265 (smaller)")
		crf      = fs.Int("crf", 18, "quality target, 0-51; lower is better (not comparable between codecs)")
		preset   = fs.String("preset", "slow", "software encoder speed/efficiency preset")
		vaapi    = fs.String("vaapi-device", "/dev/dri/renderD128", "render node for VAAPI encoding")
		swapLR   = fs.Bool("swap-lr", false, "exchange the eyes (default: taken from the disc's own base-view marking)")
		keepTemp = fs.Bool("keep-temp", false, "leave the demuxed streams behind instead of deleting them")
		quiet    = fs.Bool("quiet", false, "only report errors")
		showVer  = fs.Bool("version", false, "print the version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: mvc2sbs [--check] [--dry-run] --input SRC --output DST.mkv\n\n"+
			"Converts a Blu-ray 3D (MVC) source into a side-by-side MKV that an\n"+
			"ordinary decoder can play. External tools do the work; run --check to\n"+
			"see which are installed.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *showVer {
		fmt.Fprintf(stdout, "mvc2sbs %s\n", pversion.Resolve(version))
		return 0
	}

	goos := runtime.GOOS
	ctx := context.Background()

	cod := mvc.Codec(*codec)
	if !cod.Valid() {
		fmt.Fprintf(stderr, "mvc2sbs: unknown codec %q\n", cod)
		return 2
	}
	enc := mvc.ParseEncoder(*encoder)
	if !mvc.SupportsEncoder(goos, enc) {
		fmt.Fprintf(stderr, "mvc2sbs: encoder %q is not available on %s\n", enc, goos)
		return 2
	}
	if enc == mvc.EncoderAuto {
		enc = mvc.DefaultEncoder(ctx, goos, cod, *vaapi)
	}

	if *check {
		rep := mvc.Detect(ctx, goos, enc, cod)
		fmt.Fprint(stdout, rep.String())
		if !rep.OK() {
			return 1
		}
		return 0
	}

	o := mvc.DefaultOptions()
	o.Input, o.Output, o.TempDir = *input, *output, *tempDir
	o.Layout, o.Encoder, o.Codec = mvc.Layout(*layout), enc, cod
	o.CRF, o.Preset, o.VAAPIDevice = *crf, *preset, *vaapi
	o.SwapLR = *swapLR

	plan, err := mvc.BuildPlan(goos, o)
	if err != nil {
		fmt.Fprintf(stderr, "mvc2sbs: %v\n", err)
		fs.Usage()
		return 2
	}

	if *dryRun {
		fmt.Fprint(stdout, plan.String())
		return 0
	}

	// Refuse to start rather than fail hours in. A conversion is long enough
	// that a missing tool discovered at step three is a wasted evening.
	if rep := mvc.Detect(ctx, goos, enc, cod); !rep.OK() {
		fmt.Fprint(stderr, rep.String())
		fmt.Fprintf(stderr, "\nmvc2sbs: refusing to start with tools missing; see --check\n")
		return 1
	}

	report := mvc.Reporter(func(format string, args ...any) {
		fmt.Fprintf(stderr, "mvc2sbs: "+format+"\n", args...)
	})
	if *quiet {
		report = nil
	}

	runner := mvc.NewRunner(goos, o, report)
	runner.KeepTemp = *keepTemp
	// Whether --swap-lr was given, as opposed to merely defaulting to false:
	// a disc that marks its base view as the right eye sets this itself, but
	// must not override someone who said otherwise.
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "swap-lr" {
			runner.SwapLRSet = true
		}
	})
	if err := runner.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "mvc2sbs: %v\n", err)
		return 1
	}
	report.Report("done: %s", o.Output)
	return 0
}
