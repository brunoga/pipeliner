package mvc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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

	// Selected records what the probe chose, readable once Run returns. A
	// caller naming its output after the audio it got needs this: the codec
	// is not known until the source has been probed.
	Selected Selection
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

	if r.Opts.Remux {
		r.Selected = sel
		return r.remux(ctx, tmp, source, sel)
	}
	r.Report.Report("source: base view track %d, dependent view track %d, %d audio, %d subtitle",
		sel.Base.ID, sel.Dependent.ID, len(sel.Audio), len(sel.Subtitles))
	// Name the audio that was kept. With --audio-best this is the ranking's
	// decision, and a decision made on the operator's behalf should be
	// visible rather than inferred from the finished file hours later.
	for _, a := range sel.Audio {
		r.Report.Report("audio: %s", DescribeAudio(a))
	}
	r.Selected = sel

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
	if tmp == "" && r.Opts.Output != "" {
		tmp = filepath.Dir(r.Opts.Output)
	}
	if tmp == "" {
		// Only reachable from ListTracks, which needs no output file. A
		// conversion always has one, because Validate insists on it.
		tmp = os.TempDir()
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
	sel, err := SelectTracks(tracks)
	if err != nil {
		return Selection{}, err
	}
	// Filter here, before the demux: narrowing now means fewer tracks to
	// extract and less scratch space, not merely a smaller output. It is also
	// where a filter that matches nothing can still be reported cheaply —
	// before any of the hours a conversion takes have been spent.
	return sel.Apply(r.Opts.Audio, r.Opts.Subs)
}

// ListTracks resolves the source and returns everything it contains, with no
// filter applied, so an operator can see what the track filters have to work
// with.
//
// It costs what a conversion's first stage costs, which for a disc image means
// reading the image: there is no way to ask tsMuxeR what is on a playlist
// without extracting the playlist first. That is worth knowing before running
// it against a 40 GB image over a network share.
func (r *Runner) ListTracks(ctx context.Context) ([]Track, error) {
	tmp, cleanup, err := r.workDir()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	source, err := r.resolveSource(ctx, tmp)
	if err != nil {
		return nil, err
	}
	bin, err := r.resolve(toolTsMuxeR)
	if err != nil {
		return nil, err
	}
	out, runErr := exec.CommandContext(ctx, bin, source).CombinedOutput() //nolint:gosec // bin came from LookPath
	tracks, err := ParseListing(string(out))
	if err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("probing %s: %w\n%s", source, runErr, strings.TrimSpace(string(out)))
		}
		return nil, fmt.Errorf("probing %s: %w", source, err)
	}
	return tracks, nil
}

// DescribeTracks renders a track listing for --list: one line per track, with
// the track number a filter can also select on.
func DescribeTracks(tracks []Track) string {
	var b strings.Builder
	for _, t := range tracks {
		kind := "other"
		switch t.Kind() {
		case KindBaseView:
			kind = "video (base view)"
		case KindDependentView:
			kind = "video (dependent)"
		case KindAudio:
			kind = "audio"
		case KindSubtitle:
			kind = "subtitle"
		}
		lang := strings.TrimSpace(t.Lang)
		if lang == "" {
			lang = "und"
		}
		fmt.Fprintf(&b, "  %-5d %-18s %-5s %-20s %s\n", t.ID, kind, lang, t.Type, t.Info)
	}
	if b.Len() == 0 {
		return "  (no tracks found)\n"
	}
	return b.String()
}

// remux writes the selected tracks straight back out, with no re-encoding.
//
// The disc's MVC video passes through bit for bit, so this keeps the full
// stereo picture the disc carries rather than a re-encode of it. What it
// cannot do is change anything about that picture: the layout stays
// frame-compatible MVC rather than side by side, which means it needs a player
// that decodes MVC. Validate refuses the options that would imply otherwise.
func (r *Runner) remux(ctx context.Context, tmp, source string, sel Selection) error {
	bin, err := r.resolve(toolTsMuxeR)
	if err != nil {
		return err
	}
	meta := filepath.Join(tmp, "remux.meta")
	if err := os.WriteFile(meta, []byte(RemuxMeta(source, sel)), 0o600); err != nil {
		return fmt.Errorf("writing the remux description: %w", err)
	}
	r.Report.Report("remuxing %d audio and %d subtitle track(s) with the disc's own video",
		len(sel.Audio), len(sel.Subtitles))
	out, runErr := exec.CommandContext(ctx, bin, meta, r.Opts.Output).CombinedOutput() //nolint:gosec // bin came from LookPath
	if runErr != nil {
		return fmt.Errorf("remuxing: %w\n%s", runErr, strings.TrimSpace(string(out)))
	}
	return nil
}

// demuxResult names the files tsMuxeR wrote.
type demuxResult struct {
	base, dependent string
	// extras are the audio and subtitle files, in the order they go to the
	// muxer, each paired with the track it came from. The track is kept
	// because its language has to reach mkvmerge: tsMuxeR reports it and the
	// demux meta asks for it, but a file path alone cannot carry it, and an
	// output whose tracks are all untagged leaves a player no way to pick one.
	extras []extra
}

// extra is one demuxed audio or subtitle file and the track it came from.
type extra struct {
	path  string
	track Track
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
			res.extras = append(res.extras, extra{path: p, track: t})
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
	encBin, err := r.resolve(encoderTool(r.Opts.Encoder, r.Opts.Codec, r.Opts.EncodesViaFFmpeg()))
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
func (r *Runner) mux(ctx context.Context, video string, extras []extra) error {
	bin, err := r.resolve(toolMkvmerge)
	if err != nil {
		return err
	}
	extraArgs, dropped := r.extraArgs(ctx, bin, extras)
	for _, d := range dropped {
		r.Report.Report("dropping %s embedded in the %s track", d.codec, d.of)
	}
	argv := muxArgv(r.Opts.Output, video, extraArgs)
	r.Report.Report("muxing %s", r.Opts.Output)
	if out, err := exec.CommandContext(ctx, bin, argv...).CombinedOutput(); err != nil { //nolint:gosec // bin came from LookPath
		return fmt.Errorf("muxing: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// StereoMode is the Matroska StereoMode keyword every output carries.
//
// The left eye is always on the left: the decoder emits base-view-left, and a
// disc that marks its base view as the right eye is corrected by the swap
// filter before the mux — or the conversion is refused, since that filter
// needs an ffmpeg encoder. So the arrangement never varies.
//
// Both layouts are side-by-side. Half-SBS differs only in each eye being
// squeezed to half width, which is the same arrangement and so the same flag;
// Matroska has no way to say "full" or "half" and does not need one, because
// the frame's own dimensions say it.
const StereoMode = "side_by_side_left_first"

// muxArgv builds the mkvmerge command line.
//
// The video carries the StereoMode flag, so a player need not infer 3D from
// the filename or be told by hand. Kodi and CoreELEC read it and can then emit
// HDMI frame-packed 3D, which is what carries full resolution to each eye;
// without it the file is an unusually wide 2D video, and a player that squeezes
// it into a half-SBS output throws away half the horizontal detail the
// conversion just spent hours preserving.
func muxArgv(output, video string, extraArgs []string) []string {
	argv := []string{"-o", output, "--stereo-mode", "0:" + StereoMode, video}
	return append(argv, extraArgs...)
}

// droppedTrack names a stream left out of the mux, for reporting.
type droppedTrack struct{ codec, of string }

// extraArgs renders the audio and subtitle inputs for mkvmerge: for each
// demuxed file, the disc track it was demuxed from, tagged with the language
// tsMuxeR reported.
//
// A demuxed file is not reliably one track. A Blu-ray TrueHD stream carries an
// embedded AC-3 core for players that cannot decode TrueHD, and tsMuxeR writes
// the pair as one file — ".ac3+thd" — which mkvmerge presents as two tracks.
// Passing that file through whole puts an extra audio track in the output that
// the disc never listed and the probe never promised, and tagging only track 0
// leaves it with no language at all, so a player sees an unidentified track
// beside the one it was told about.
//
// So each file is identified first and only its primary track is taken. The
// core is dropped rather than carried: it is the same audio, lossily, and the
// output should hold the tracks the disc has.
//
// A track the source gave no language for is passed untagged rather than
// guessed at: an absent tag already means undetermined in Matroska, and
// claiming a language the disc never stated would be worse than saying
// nothing.
func (r *Runner) extraArgs(ctx context.Context, bin string, extras []extra) ([]string, []droppedTrack) {
	var (
		argv    []string
		dropped []droppedTrack
	)
	for _, e := range extras {
		id, lang := 0, strings.TrimSpace(e.track.Lang)
		if have, err := identify(ctx, bin, e.path); err == nil {
			primary, rest := primaryTrack(e.track, have)
			id = primary.ID
			if r.Opts.KeepFallback {
				// Keeping the core means tagging it too. Leaving it
				// unidentified beside the track it accompanies is the defect
				// this whole path exists to avoid, and it applies just as much
				// to a track kept on purpose as to one kept by accident.
				for _, x := range rest {
					if lang != "" {
						argv = append(argv, "--language", strconv.Itoa(x.ID)+":"+lang)
					}
				}
			} else {
				for _, x := range rest {
					dropped = append(dropped, droppedTrack{codec: x.Codec, of: primary.Codec})
				}
				argv = append(argv, selectorFor(primary, len(have) > 1)...)
			}
		}
		// Identification failing is not fatal: the file still muxes, it just
		// goes in whole and the language reaches only its first track. That
		// is the behaviour before any of this, so it is a safe fallback.
		if lang != "" {
			argv = append(argv, "--language", strconv.Itoa(id)+":"+lang)
		}
		argv = append(argv, e.path)
	}
	return argv, dropped
}

// selectorFor limits a file to its primary track. Nothing is emitted when the
// file holds one track, so the common case produces the same command as
// before and mkvmerge is not asked to filter what needs no filtering.
func selectorFor(primary muxTrack, needed bool) []string {
	if !needed {
		return nil
	}
	flag := ""
	switch primary.Kind {
	case "audio":
		flag = "--audio-tracks"
	case "subtitles":
		flag = "--subtitle-tracks"
	default:
		return nil
	}
	return []string{flag, strconv.Itoa(primary.ID)}
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
