// Command bd3d2sbs converts a frame-packed Blu-ray 3D source (MVC) into a
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
// It follows the pipeline of bd3d2sbs
// (https://github.com/Michal-Szczepaniak/bd3d2sbs), a Linux port of the
// Windows tool BD3D2MK3D, and is a Go rewrite of that shell script with the
// platform differences made explicit.
//
// Usage:
//
//	bd3d2sbs --check
//	bd3d2sbs --dry-run --input disc.iso --output "Life of Pi (2012).mkv"
//	bd3d2sbs --input disc.iso --output "Life of Pi (2012).mkv" --layout full
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/brunoga/pipeliner/internal/mvc"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(argv []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("bd3d2sbs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		check   = fs.Bool("check", false, "report which external tools are present and which are missing, then exit")
		dryRun  = fs.Bool("dry-run", false, "print the commands that would run, without running them")
		input   = fs.String("input", "", "source: a .iso, a BDMV directory, or an MKV from MakeMKV")
		output  = fs.String("output", "", "destination .mkv")
		tempDir = fs.String("temp", "", "scratch directory for demuxed streams (default: alongside the output)")
		layout  = fs.String("layout", string(mvc.LayoutFullSBS), "full (1080p per eye) or half (960p per eye, ~half the size)")
		encoder = fs.String("encoder", string(mvc.EncoderAuto), "auto, x264, vaapi, videotoolbox or nvenc")
		crf     = fs.Int("crf", 18, "quality target, 0-51; lower is better")
		preset  = fs.String("preset", "slow", "x264 speed/efficiency preset")
		vaapi   = fs.String("vaapi-device", "/dev/dri/renderD128", "render node for VAAPI encoding")
		showVer = fs.Bool("version", false, "print the version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: bd3d2sbs [--check] [--dry-run] --input SRC --output DST.mkv\n\n"+
			"Converts a Blu-ray 3D (MVC) source into a side-by-side MKV that an\n"+
			"ordinary decoder can play. External tools do the work; run --check to\n"+
			"see which are installed.\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *showVer {
		fmt.Fprintf(stdout, "bd3d2sbs %s\n", version)
		return 0
	}

	goos := runtime.GOOS
	ctx := context.Background()

	enc := mvc.Encoder(*encoder)
	if !mvc.SupportsEncoder(goos, enc) {
		fmt.Fprintf(stderr, "bd3d2sbs: encoder %q is not available on %s\n", enc, goos)
		return 2
	}
	if enc == mvc.EncoderAuto {
		enc = mvc.DefaultEncoder(ctx, goos)
	}

	if *check {
		rep := mvc.Detect(ctx, goos, enc)
		fmt.Fprint(stdout, rep.String())
		if !rep.OK() {
			return 1
		}
		return 0
	}

	o := mvc.DefaultOptions()
	o.Input, o.Output, o.TempDir = *input, *output, *tempDir
	o.Layout, o.Encoder, o.CRF, o.Preset, o.VAAPIDevice = mvc.Layout(*layout), enc, *crf, *preset, *vaapi

	plan, err := mvc.BuildPlan(goos, o)
	if err != nil {
		fmt.Fprintf(stderr, "bd3d2sbs: %v\n", err)
		fs.Usage()
		return 2
	}

	if *dryRun {
		fmt.Fprint(stdout, plan.String())
		return 0
	}

	// Refuse to start rather than fail hours in. A conversion is long enough
	// that a missing tool discovered at step three is a wasted evening.
	if rep := mvc.Detect(ctx, goos, enc); !rep.OK() {
		fmt.Fprint(stderr, rep.String())
		fmt.Fprintf(stderr, "\nbd3d2sbs: refusing to start with tools missing; see --check\n")
		return 1
	}

	fmt.Fprintf(stderr, "bd3d2sbs: the conversion runner is not implemented yet.\n"+
		"Every tool it needs is present and the plan below is what it would run.\n"+
		"Use --dry-run to see this without the toolchain check.\n\n")
	fmt.Fprint(stdout, plan.String())
	return 3
}
