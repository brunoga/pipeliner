package probe

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	mvcprobe "github.com/brunoga/mvc/probe"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/torrentpeek"
)

// fakeSampler stands in for a torrent: a byte slice plus the set of pieces
// that have been "fetched". Reads outside those pieces fail the way
// torrentpeek does, which is what drives the retry loop.
type fakeSampler struct {
	data    []byte
	piece   int64
	have    map[int]bool
	fetches []int // every piece Fetch was asked for, in order
	failAt  int   // Fetch returns an error on the nth call (1-based); 0 never
	calls   int
}

func newFakeSampler(size, piece int64) *fakeSampler {
	return &fakeSampler{data: make([]byte, size), piece: piece, have: map[int]bool{}}
}

func (f *fakeSampler) Size() int64        { return int64(len(f.data)) }
func (f *fakeSampler) PieceLength() int64 { return f.piece }
func (f *fakeSampler) PieceOf(off int64) int {
	i := int(off / f.piece)
	if n := f.numPieces(); i >= n {
		return n - 1
	}
	return i
}
func (f *fakeSampler) numPieces() int {
	n := int(int64(len(f.data)) / f.piece)
	if int64(len(f.data))%f.piece != 0 {
		n++
	}
	return n
}
func (f *fakeSampler) Ends() []int {
	if n := f.numPieces(); n > 1 {
		return []int{0, n - 1}
	}
	return []int{0}
}
func (f *fakeSampler) FetchedPieces() int { return len(f.have) }

func (f *fakeSampler) Fetch(_ context.Context, pieces ...int) error {
	f.calls++
	if f.failAt != 0 && f.calls == f.failAt {
		return errors.New("swarm unreachable")
	}
	for _, i := range pieces {
		f.have[i] = true
		f.fetches = append(f.fetches, i)
	}
	return nil
}

func (f *fakeSampler) ReadAt(b []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	var eof error
	if rem := int64(len(f.data)) - off; int64(len(b)) > rem {
		b, eof = b[:rem], io.EOF
	}
	for i := f.PieceOf(off); i <= f.PieceOf(off+int64(len(b))-1); i++ {
		if !f.have[i] {
			return 0, &torrentpeek.MissingPieceError{Offset: off, Piece: i}
		}
	}
	copy(b, f.data[off:off+int64(len(b))])
	return len(b), eof
}

func testPlugin(t *testing.T, cfg map[string]any) *probePlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*probePlugin)
}

// scriptedProber fails with MissingDataError at the given offsets in turn,
// then succeeds. That is the real shape: the prober reads the image's
// structure, hits a byte it has not been given, and says which one.
type scriptedProber struct {
	missing []int64
	calls   int
}

func (sp *scriptedProber) image(_ io.ReaderAt, _ int64, label string) (*mvcprobe.Result, error) {
	defer func() { sp.calls++ }()
	if sp.calls < len(sp.missing) {
		return nil, &mvcprobe.MissingDataError{Offset: sp.missing[sp.calls], Length: 2048, Err: io.EOF}
	}
	return &mvcprobe.Result{Kind: mvcprobe.KindDisc, Source: label, Is3D: true, Layout: mvcprobe.LayoutMVC}, nil
}

// TestFetchLoopStartsAtTheEnds pins the shape of the first request: a disc
// image keeps its UDF directory at the front and its BDMV metadata at the
// back, so both ends go in one round rather than one piece at a time.
func TestFetchLoopStartsAtTheEnds(t *testing.T) {
	p := testPlugin(t, nil)
	sp := &scriptedProber{}
	p.probeImage = sp.image
	f := newFakeSampler(100<<20, 16<<20) // 7 pieces

	if _, err := p.readWithRetries(context.Background(), f, "disc.iso"); err != nil {
		t.Fatal(err)
	}
	if len(f.fetches) != 2 || f.fetches[0] != 0 || f.fetches[1] != f.numPieces()-1 {
		t.Errorf("fetches = %v, want exactly the first and last piece", f.fetches)
	}
}

// TestFetchLoopRetriesThePieceTheProberNames is the Moana case: a disc with
// hundreds of playlists spills its metadata past the last 16 MiB piece, so
// assuming two pieces fails on real discs. The prober says which byte it
// wants; the loop turns that into a piece and fetches it.
func TestFetchLoopRetriesThePieceTheProberNames(t *testing.T) {
	p := testPlugin(t, nil)
	f := newFakeSampler(100<<20, 16<<20) // 7 pieces, last is 6
	// First attempt wants a byte in piece 5, the second-to-last.
	wantPiece := 5
	sp := &scriptedProber{missing: []int64{int64(wantPiece)*(16<<20) + 1234}}
	p.probeImage = sp.image

	res, err := p.readWithRetries(context.Background(), f, "disc.iso")
	if err != nil {
		t.Fatalf("the loop should have fetched the named piece and succeeded: %v", err)
	}
	if res == nil || !res.Is3D {
		t.Fatalf("result = %+v", res)
	}
	if len(f.fetches) != 3 || f.fetches[2] != wantPiece {
		t.Errorf("fetches = %v, want the ends then piece %d", f.fetches, wantPiece)
	}
	if sp.calls != 2 {
		t.Errorf("prober called %d times, want 2", sp.calls)
	}
}

// TestFetchLoopStopsAtMaxPieces is the cost guard: these bytes are real and
// count against a private tracker's ratio, so a probe that keeps asking must
// be cut off rather than walking the image a piece at a time.
func TestFetchLoopStopsAtMaxPieces(t *testing.T) {
	p := testPlugin(t, map[string]any{"max_pieces": 3})
	f := newFakeSampler(500<<20, 16<<20)
	// Always wants another piece, one further along each time.
	var offs []int64
	for i := range 20 {
		offs = append(offs, int64(i+2)*(16<<20))
	}
	sp := &scriptedProber{missing: offs}
	p.probeImage = sp.image

	_, err := p.readWithRetries(context.Background(), f, "disc.iso")
	if err == nil {
		t.Fatal("a probe that never succeeds must end in an error")
	}
	if !strings.Contains(err.Error(), "max_pieces") {
		t.Errorf("error = %q, want it to name the limit that stopped it", err)
	}
	if f.FetchedPieces() > 3 {
		t.Errorf("fetched %d pieces, want at most the configured 3", f.FetchedPieces())
	}
}

// A parse failure is not a missing-data failure. Zero-filled bytes are present
// bytes, so the prober reads them and rejects them — and the loop must report
// that rather than fetching the rest of the image hoping it improves.
func TestFetchLoopDoesNotRetryAParseError(t *testing.T) {
	p := testPlugin(t, nil)
	calls := 0
	p.probeImage = func(io.ReaderAt, int64, string) (*mvcprobe.Result, error) {
		calls++
		return nil, errors.New("unexpected anchor volume pointer tag")
	}
	f := newFakeSampler(100<<20, 16<<20)

	if _, err := p.readWithRetries(context.Background(), f, "disc.iso"); err == nil {
		t.Fatal("want the parse error")
	}
	if calls != 1 {
		t.Errorf("prober called %d times, want 1: a parse error is final", calls)
	}
	if f.FetchedPieces() != 2 {
		t.Errorf("fetched %d pieces, want just the 2 ends", f.FetchedPieces())
	}
}

// A fetch that fails outright — an unreachable swarm — is reported rather than
// retried forever.
func TestFetchLoopReportsFetchFailure(t *testing.T) {
	p := testPlugin(t, nil)
	f := newFakeSampler(100<<20, 16<<20)
	f.failAt = 1

	if _, err := p.readWithRetries(context.Background(), f, "disc.iso"); err == nil {
		t.Fatal("want the fetch error")
	} else if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error = %q, want the underlying fetch failure", err)
	}
}

// A .mkv is probed through the Matroska path, over the first piece only:
// Matroska states its tracks up front, so there is nothing at the tail to get.
func TestMatroskaTakesTheMatroskaPath(t *testing.T) {
	p := testPlugin(t, nil)
	var gotLabel string
	p.probeMatroska = func(_ io.Reader, label string) (*mvcprobe.Result, error) {
		gotLabel = label
		return &mvcprobe.Result{Kind: mvcprobe.KindMatroska, Layout: mvcprobe.Layout2D}, nil
	}
	p.probeImage = func(io.ReaderAt, int64, string) (*mvcprobe.Result, error) {
		t.Error("a .mkv must not be probed as a disc image")
		return nil, errors.New("wrong path")
	}
	f := newFakeSampler(100<<20, 16<<20)

	res, err := p.readWithRetries(context.Background(), f, "Some.Film.2024.1080p.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != mvcprobe.KindMatroska || gotLabel != "Some.Film.2024.1080p.mkv" {
		t.Errorf("kind=%v label=%q", res.Kind, gotLabel)
	}
}

func TestMissingOffset(t *testing.T) {
	if _, ok := missingOffset(errors.New("plain")); ok {
		t.Error("a plain error names no offset")
	}
	// The prober's own error is preferred, since it knows which bytes the
	// format needed rather than which read happened to fail.
	md := &mvcprobe.MissingDataError{Offset: 4242, Length: 2048}
	if off, ok := missingOffset(md); !ok || off != 4242 {
		t.Errorf("probe error gave (%d, %v), want (4242, true)", off, ok)
	}
	mp := &torrentpeek.MissingPieceError{Offset: 99, Piece: 3}
	if off, ok := missingOffset(mp); !ok || off != 99 {
		t.Errorf("reader error gave (%d, %v), want (99, true)", off, ok)
	}
	// Wrapped, as it arrives in practice.
	if off, ok := missingOffset(errors.Join(errors.New("ctx"), md)); !ok || off != 4242 {
		t.Errorf("wrapped probe error gave (%d, %v)", off, ok)
	}
}

// TestApplyDiscResult covers the stamping of a 3D disc, including the two
// things the release name routinely gets wrong.
func TestApplyDiscResult(t *testing.T) {
	e := entry.New("Some Disc 2018 1080p 3D Complete Bluray", "http://x/t.torrent")
	e.Set(entry.FieldTorrentFileSize, int64(49585389568)) // 46.18 GiB
	res := &mvcprobe.Result{
		Kind:          mvcprobe.KindDisc,
		Duration:      2*time.Hour + 14*time.Minute + 32*time.Second,
		Is3D:          true,
		Layout:        mvcprobe.LayoutMVC,
		BaseViewRight: true,
		Video: []mvcprobe.VideoTrack{
			{Codec: "H.264", Width: 1920, Height: 1080},
			{Codec: "MVC"}, // the dependent view: a disc does not state its size
		},
		Audio: []mvcprobe.AudioTrack{
			{Codec: "TRUE-HD", Language: "eng"},
			{Codec: "DTS", Language: "fra"},
			{Codec: "AC3", Language: "eng"}, // duplicate language
			{Codec: "DTS"},                  // untagged
		},
		Subtitle: []mvcprobe.SubtitleTrack{{Codec: "PGS", Language: "eng"}, {Codec: "PGS", Language: "por"}},
	}
	apply(e, res)

	if !e.GetBool(entry.FieldProbeOK) || e.GetString(entry.FieldProbeKind) != "disc" {
		t.Errorf("ok=%v kind=%q", e.GetBool(entry.FieldProbeOK), e.GetString(entry.FieldProbeKind))
	}
	if !e.GetBool(entry.FieldProbeIs3D) || e.GetString(entry.FieldProbe3DLayout) != "mvc" {
		t.Errorf("3d=%v layout=%q", e.GetBool(entry.FieldProbeIs3D), e.GetString(entry.FieldProbe3DLayout))
	}
	if !e.GetBool(entry.FieldProbeBaseViewRight) {
		t.Error("the disc says its base view is the right eye; that must survive to the conversion")
	}
	// The first track that states a size wins, so the dependent view's absent
	// one does not blank it.
	if e.GetInt(entry.FieldProbeWidth) != 1920 || e.GetInt(entry.FieldProbeHeight) != 1080 {
		t.Errorf("geometry = %dx%d, want 1920x1080", e.GetInt(entry.FieldProbeWidth), e.GetInt(entry.FieldProbeHeight))
	}
	if e.GetInt(entry.FieldProbeDurationSec) != 8072 {
		t.Errorf("duration = %d s, want 8072", e.GetInt(entry.FieldProbeDurationSec))
	}
	// 49585389568 bytes * 8 / 8072 s = 49.1 Mbps, measured rather than
	// estimated from an enrichment runtime.
	if got, ok := e.Fields[entry.FieldProbeBitrateMbps].(float64); !ok || got < 49.0 || got > 49.2 {
		t.Errorf("bitrate = %v, want about 49.1 Mbps", e.Fields[entry.FieldProbeBitrateMbps])
	}
	langs, _ := e.Fields[entry.FieldProbeAudioLanguages].([]string)
	if len(langs) != 2 || langs[0] != "eng" || langs[1] != "fra" {
		t.Errorf("audio languages = %v, want eng then fra: deduped, untagged dropped, order kept", langs)
	}
	codecs, _ := e.Fields[entry.FieldProbeAudioCodecs].([]string)
	if len(codecs) != 4 {
		t.Errorf("audio codecs = %v, want one per track", codecs)
	}
	subs, _ := e.Fields[entry.FieldProbeSubtitleLanguages].([]string)
	if len(subs) != 2 {
		t.Errorf("subtitle languages = %v", subs)
	}
}

// TestApply2DIsExplicit is the generalisation requirement: a 2D source must
// say so, so a pipeline hunting 2D films can use the same probe as one hunting
// MVC discs.
func TestApply2DIsExplicit(t *testing.T) {
	e := entry.New("Some Film 2024 1080p BluRay x264", "http://x/t.torrent")
	res := &mvcprobe.Result{
		Kind:     mvcprobe.KindMatroska,
		Duration: 100 * time.Minute,
		Is3D:     false,
		Layout:   mvcprobe.Layout2D,
		Video:    []mvcprobe.VideoTrack{{Codec: "H.264", Width: 1920, Height: 1080}},
	}
	apply(e, res)

	v, ok := e.Fields[entry.FieldProbeIs3D]
	if !ok {
		t.Fatal("probe_is_3d must be set for a 2D source, not merely absent")
	}
	if v != false {
		t.Errorf("probe_is_3d = %v, want false", v)
	}
	if got := e.GetString(entry.FieldProbe3DLayout); got != "2d" {
		t.Errorf("layout = %q, want 2d", got)
	}
	// BaseViewRight is a disc concept; a Matroska result must not imply one.
	if _, ok := e.Fields[entry.FieldProbeBaseViewRight]; ok {
		t.Error("probe_base_view_right must not be set for a Matroska source")
	}
}

// A side-by-side encode is the case the title parser keeps getting wrong: a
// 3840x1080 frame is two 1080p eyes, and the container says so.
func TestApplySideBySide(t *testing.T) {
	e := entry.New("Some Film 2024 3D COMPLETE BLURAY FULL-SBS x264", "http://x/t.torrent")
	apply(e, &mvcprobe.Result{
		Kind: mvcprobe.KindMatroska, Is3D: true, Layout: mvcprobe.LayoutSideBySide,
		Video: []mvcprobe.VideoTrack{{Codec: "HEVC", Width: 3840, Height: 1080}},
	})
	if got := e.GetString(entry.FieldProbe3DLayout); got != "sbs" {
		t.Errorf("layout = %q, want sbs", got)
	}
	if e.GetInt(entry.FieldProbeWidth) != 3840 {
		t.Errorf("width = %d, want the coded 3840", e.GetInt(entry.FieldProbeWidth))
	}
}

// Nothing is invented. A container that states no duration gets no duration
// and therefore no bitrate, rather than a zero that reads as a real answer.
func TestApplyLeavesUnstatedFieldsUnset(t *testing.T) {
	e := entry.New("Some Film", "http://x/t.torrent")
	e.Set(entry.FieldTorrentFileSize, int64(1<<30))
	apply(e, &mvcprobe.Result{Kind: mvcprobe.KindMatroska, Video: []mvcprobe.VideoTrack{{Codec: "HEVC"}}})

	for _, f := range []string{
		entry.FieldProbeDurationSec, entry.FieldProbeBitrateMbps,
		entry.FieldProbeWidth, entry.FieldProbeHeight,
		entry.FieldProbeAudioCodecs, entry.FieldProbeAudioLanguages,
	} {
		if _, ok := e.Fields[f]; ok {
			t.Errorf("%s was set from a result that does not state it", f)
		}
	}
	if e.GetString(entry.FieldProbeVideoCodec) != "HEVC" {
		t.Error("a stated codec should still be set")
	}
}

func TestLayoutAndKindNames(t *testing.T) {
	for l, want := range map[mvcprobe.Layout]string{
		mvcprobe.Layout2D: "2d", mvcprobe.LayoutMVC: "mvc",
		mvcprobe.LayoutSideBySide: "sbs", mvcprobe.LayoutTopBottom: "tab",
		mvcprobe.LayoutOther: "other",
	} {
		if got := layoutName(l); got != want {
			t.Errorf("layoutName(%d) = %q, want %q", l, got, want)
		}
	}
	for k, want := range map[mvcprobe.Kind]string{
		mvcprobe.KindUnknown: "unknown", mvcprobe.KindDisc: "disc", mvcprobe.KindMatroska: "matroska",
	} {
		if got := kindName(k); got != want {
			t.Errorf("kindName(%d) = %q, want %q", k, got, want)
		}
	}
	// Every name the plugin can emit is declared in the field metadata, so the
	// condition editor can offer them.
	for _, l := range []mvcprobe.Layout{mvcprobe.Layout2D, mvcprobe.LayoutMVC, mvcprobe.LayoutSideBySide, mvcprobe.LayoutTopBottom, mvcprobe.LayoutOther} {
		if !declaredValue(entry.FieldProbe3DLayout, layoutName(l)) {
			t.Errorf("layout %q is not in the field metadata's KnownValues", layoutName(l))
		}
	}
}

func declaredValue(field, value string) bool {
	for _, m := range entry.KnownFields {
		if m.Name != field {
			continue
		}
		for _, v := range m.KnownValues {
			if v == value {
				return true
			}
		}
	}
	return false
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{"timeout": "nope"}); len(errs) == 0 {
		t.Error("an unparseable timeout should be rejected")
	}
	if errs := validate(map[string]any{"max_pieces": 0}); len(errs) == 0 {
		t.Error("max_pieces below 1 should be rejected")
	}
	if errs := validate(map[string]any{"wat": 1}); len(errs) == 0 {
		t.Error("an unknown key should be rejected")
	}
	if errs := validate(map[string]any{"timeout": "90s", "max_pieces": 4, "require": true}); len(errs) != 0 {
		t.Errorf("a valid config should pass: %v", errs)
	}
}

func TestDefaults(t *testing.T) {
	p := testPlugin(t, nil)
	if p.timeout != defaultTimeout || p.maxPieces != defaultMaxPieces || p.require {
		t.Errorf("defaults = %s/%d/require=%v", p.timeout, p.maxPieces, p.require)
	}
}
