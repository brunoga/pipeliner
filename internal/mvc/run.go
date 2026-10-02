package mvc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Reporter receives progress. A conversion runs for hours, so it has to say
// what it is doing; nil discards.
type Reporter func(format string, args ...any)

// Report emits a line, doing nothing when the reporter is nil.
func (r Reporter) Report(format string, args ...any) {
	if r != nil {
		r(format, args...)
	}
}

// Runner executes a conversion.
type Runner struct {
	Opts Options
	// GOOS is the platform whose toolchain and encoders apply.
	GOOS string
	// Report receives progress lines.
	Report Reporter
	// KeepTemp leaves the demuxed streams behind, for looking at a bad result
	// without paying for the demux again.
	KeepTemp bool
	// SwapLRSet records that the operator gave --swap-lr explicitly, which
	// stops the disc's own base-view marking from overriding them.
	SwapLRSet bool

	// tool resolves a program name to a path. Indirected for tests.
	tool func(string) (string, error)
}

// NewRunner returns a runner for opts.
func NewRunner(goos string, opts Options, report Reporter) *Runner {
	return &Runner{Opts: opts, GOOS: goos, Report: report, tool: LookPath}
}

// Run performs the conversion: probe the source, demux both views, interleave
// them into the decoder, encode the stacked frames, and mux the result with the
// audio and subtitles the source carried.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.Opts.Validate(r.GOOS); err != nil {
		return err
	}
	tmp, cleanup, err := r.workDir()
	if err != nil {
		return err
	}
	defer cleanup()

	// An image or a disc folder is resolved to a concrete playlist first, so
	// everything after this works on one file as before.
	source, err := r.resolveSource(ctx, tmp)
	if err != nil {
		return err
	}

	sel, err := r.probe(ctx, source)
	if err != nil {
		return err
	}
	r.Report.Report("source: base view track %d, dependent view track %d, %d audio, %d subtitle",
		sel.Base.ID, sel.Dependent.ID, len(sel.Audio), len(sel.Subtitles))

	demuxed, err := r.demux(ctx, tmp, source, sel)
	if err != nil {
		return err
	}

	video := filepath.Join(tmp, "stacked"+r.Opts.Codec.streamExt())
	if err := r.decodeAndEncode(ctx, demuxed.base, demuxed.dependent, video); err != nil {
		return err
	}

	return r.mux(ctx, video, demuxed.extras)
}

// workDir returns the scratch directory and a cleanup. A conversion writes tens
// of gigabytes of demuxed streams, so leaving them behind by accident is not a
// small mistake.
func (r *Runner) workDir() (string, func(), error) {
	tmp := r.Opts.TempDir
	if tmp == "" {
		tmp = filepath.Dir(r.Opts.Output)
	}
	dir, err := os.MkdirTemp(tmp, "mvc2sbs-")
	if err != nil {
		return "", func() {}, fmt.Errorf("creating a work directory under %s: %w", tmp, err)
	}
	return dir, func() {
		if r.KeepTemp {
			r.Report.Report("keeping %s", dir)
			return
		}
		if err := os.RemoveAll(dir); err != nil {
			r.Report.Report("could not remove %s: %v", dir, err)
		}
	}, nil
}

// resolveSource turns whatever was given into the one file tsMuxeR will read.
//
//   - a disc image: the files that matter are extracted and a playlist chosen
//   - a BDMV directory (or its parent): a playlist is chosen
//   - anything else: used as given
//
// Choosing the playlist here rather than asking the operator to is the point of
// the exercise: a disc holds dozens, most of them trailers and menus, and an
// unattended conversion cannot be expected to guess.
func (r *Runner) resolveSource(ctx context.Context, tmp string) (string, error) {
	in := r.Opts.Input
	if LooksLikeISO(in) {
		r.Report.Report("reading the disc image (no mount needed)")
		bdmv, err := ExtractBDMV(ctx, in, filepath.Join(tmp, "disc"), r.Report)
		if err != nil {
			return "", err
		}
		return r.choose(ctx, bdmv)
	}
	st, err := os.Stat(in)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", in, err)
	}
	if !st.IsDir() {
		return in, nil
	}
	// A directory is either a BDMV or the folder holding one.
	bdmv := in
	if filepath.Base(strings.ToUpper(in)) != "BDMV" {
		bdmv = filepath.Join(in, "BDMV")
	}
	if _, err := os.Stat(bdmv); err != nil {
		return "", fmt.Errorf("%s is a directory but holds no BDMV", in)
	}
	return r.choose(ctx, bdmv)
}

// choose picks the playlist, probing each with tsMuxeR.
func (r *Runner) choose(ctx context.Context, bdmv string) (string, error) {
	bin, err := r.resolve(toolTsMuxeR)
	if err != nil {
		return "", err
	}
	pl, err := ChoosePlaylist(ctx, func(ctx context.Context, path string) (string, error) {
		out, err := exec.CommandContext(ctx, bin, path).CombinedOutput() //nolint:gosec // bin came from LookPath
		return string(out), err
	}, bdmv, r.Report)
	if err != nil {
		return "", err
	}
	// Take the eye order from the disc unless it was given explicitly. Doing
	// it the other way round would silently ignore an operator who had watched
	// the result and knows better.
	if pl.KnownEye && !r.SwapLRSet {
		r.Opts.SwapLR = pl.BaseViewIsRight
	}
	return pl.Path, nil
}

// probe asks tsMuxeR what the source contains.
func (r *Runner) probe(ctx context.Context, source string) (Selection, error) {
	bin, err := r.resolve(toolTsMuxeR)
	if err != nil {
		return Selection{}, err
	}
	r.Report.Report("probing %s", source)
	// tsMuxeR exits non-zero on some sources it nonetheless describes, so the
	// output is parsed whatever the status and the error only surfaces when
	// there is nothing to parse.
	out, runErr := exec.CommandContext(ctx, bin, source).CombinedOutput() //nolint:gosec // bin came from LookPath
	tracks, err := ParseListing(string(out))
	if err != nil {
		if runErr != nil {
			return Selection{}, fmt.Errorf("probing %s: %w\n%s", source, runErr, strings.TrimSpace(string(out)))
		}
		return Selection{}, fmt.Errorf("probing %s: %w", source, err)
	}
	return SelectTracks(tracks)
}

// demuxResult names the files tsMuxeR wrote.
type demuxResult struct {
	base, dependent string
	// extras are the audio and subtitle files, in the order they go to the muxer.
	extras []string
}

// demux extracts the selected tracks.
func (r *Runner) demux(ctx context.Context, tmp, source string, sel Selection) (demuxResult, error) {
	bin, err := r.resolve(toolTsMuxeR)
	if err != nil {
		return demuxResult{}, err
	}
	metaPath := filepath.Join(tmp, "demux.meta")
	if err := os.WriteFile(metaPath, []byte(DemuxMeta(source, sel)), 0o600); err != nil {
		return demuxResult{}, fmt.Errorf("writing the demux meta file: %w", err)
	}
	r.Report.Report("demuxing both views%s", pluralExtras(sel))
	if out, err := exec.CommandContext(ctx, bin, metaPath, tmp).CombinedOutput(); err != nil { //nolint:gosec // bin came from LookPath
		return demuxResult{}, fmt.Errorf("demuxing: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	written, err := os.ReadDir(tmp)
	if err != nil {
		return demuxResult{}, err
	}
	var names []string
	for _, e := range written {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	res := demuxResult{}
	// tsMuxeR names a demuxed track "<source>.track_<id>[_<lang>].<ext>", so the
	// track number is what identifies a file rather than its position.
	find := func(id int) string {
		want := fmt.Sprintf(".track_%d", id)
		for _, n := range names {
			if strings.Contains(n, want+".") || strings.Contains(n, want+"_") {
				return filepath.Join(tmp, n)
			}
		}
		return ""
	}
	if res.base = find(sel.Base.ID); res.base == "" {
		return demuxResult{}, fmt.Errorf("the demux produced no file for base view track %d (wrote: %s)",
			sel.Base.ID, strings.Join(names, ", "))
	}
	if res.dependent = find(sel.Dependent.ID); res.dependent == "" {
		return demuxResult{}, fmt.Errorf("the demux produced no file for dependent view track %d (wrote: %s)",
			sel.Dependent.ID, strings.Join(names, ", "))
	}
	for _, t := range append(append([]Track(nil), sel.Audio...), sel.Subtitles...) {
		if p := find(t.ID); p != "" {
			res.extras = append(res.extras, p)
		} else {
			// A missing extra costs a language, not the film, so it is reported
			// and the conversion goes on.
			r.Report.Report("warning: no demuxed file for %s track %d; it will be missing from the output",
				t.StreamID, t.ID)
		}
	}
	return res, nil
}

func pluralExtras(sel Selection) string {
	n := len(sel.Audio) + len(sel.Subtitles)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" and %d other track(s)", n)
}

// decodeAndEncode streams the interleaved views through the decoder and into
// the encoder. Neither the combined stream nor the raw frames touch the disk:
// for a feature film that is tens of gigabytes and hundreds respectively.
func (r *Runner) decodeAndEncode(ctx context.Context, base, dependent, out string) error {
	decBin, err := r.resolve(toolEdge264)
	if err != nil {
		return err
	}
	encStep := encodeStep(r.Opts, out)
	encBin, err := r.resolve(encoderTool(r.Opts.Encoder, r.Opts.Codec))
	if err != nil {
		return err
	}

	baseBytes, err := os.ReadFile(base)
	if err != nil {
		return fmt.Errorf("reading the base view: %w", err)
	}
	depBytes, err := os.ReadFile(dependent)
	if err != nil {
		return fmt.Errorf("reading the dependent view: %w", err)
	}

	dec := exec.CommandContext(ctx, decBin, "-y", "-k", "-O", "-") //nolint:gosec // decBin came from LookPath
	enc := exec.CommandContext(ctx, encBin, encStep.Argv[1:]...)   //nolint:gosec // encBin came from LookPath

	stdin, err := dec.StdinPipe()
	if err != nil {
		return err
	}
	pipe, err := dec.StdoutPipe()
	if err != nil {
		return err
	}
	enc.Stdin = pipe
	var decErr, encErr strings.Builder
	dec.Stderr = &decErr
	enc.Stderr = &encErr

	r.Report.Report("decoding and encoding (%s, %s)", r.Opts.Codec, r.Opts.Encoder)
	if err := dec.Start(); err != nil {
		return fmt.Errorf("starting the decoder: %w", err)
	}
	if err := enc.Start(); err != nil {
		_ = stdin.Close()
		_ = dec.Process.Kill()
		return fmt.Errorf("starting the encoder: %w", err)
	}

	// Feed the interleaved stream. A write error usually means the decoder has
	// already exited, and its own message is the useful one, so this is kept
	// and only reported if the decoder itself says nothing.
	feedErr := Interleave(stdin, baseBytes, depBytes)
	if cerr := stdin.Close(); feedErr == nil && cerr != nil {
		feedErr = cerr
	}

	decWait := dec.Wait()
	encWait := enc.Wait()

	switch {
	case decWait != nil:
		return fmt.Errorf("decoding: %w\n%s", decWait, strings.TrimSpace(decErr.String()))
	case encWait != nil:
		return fmt.Errorf("encoding: %w\n%s", encWait, strings.TrimSpace(encErr.String()))
	case feedErr != nil && feedErr != io.ErrClosedPipe:
		return fmt.Errorf("interleaving the views: %w", feedErr)
	}
	return nil
}

// mux assembles the final file.
func (r *Runner) mux(ctx context.Context, video string, extras []string) error {
	bin, err := r.resolve(toolMkvmerge)
	if err != nil {
		return err
	}
	argv := append([]string{"-o", r.Opts.Output, video}, extras...)
	r.Report.Report("muxing %s", r.Opts.Output)
	if out, err := exec.CommandContext(ctx, bin, argv...).CombinedOutput(); err != nil { //nolint:gosec // bin came from LookPath
		return fmt.Errorf("muxing: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// resolve finds a tool, naming what is missing and what it is for rather than
// reporting "executable file not found".
func (r *Runner) resolve(t Tool) (string, error) {
	look := r.tool
	if look == nil {
		look = LookPath
	}
	for _, name := range t.Binaries {
		if p, err := look(name); err == nil {
			return p, nil
		}
	}
	hint := t.InstallHint(r.GOOS)
	if hint != "" {
		hint = "\n  try: " + hint
	}
	return "", fmt.Errorf("%s is not installed — needed to %s%s", t.Name, t.Purpose, hint)
}
