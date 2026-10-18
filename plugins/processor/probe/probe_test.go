package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	mvcprobe "github.com/brunoga/bdtools/probe"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/torrentpeek"
	"github.com/brunoga/pipeliner/quality"
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
func (f *fakeSampler) HasPiece(i int) bool { return f.have[i] }

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
			return 0, &torrentpeek.MissingPieceError{Offset: off, Length: int64(len(b)), Piece: i}
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
	length  int64 // of each missing read; 0 means 2048
	calls   int
}

func (sp *scriptedProber) image(_ io.ReaderAt, _ int64, label string) (*mvcprobe.Result, error) {
	defer func() { sp.calls++ }()
	if sp.calls < len(sp.missing) {
		n := sp.length
		if n == 0 {
			n = 2048
		}
		return nil, &mvcprobe.MissingDataError{Offset: sp.missing[sp.calls], Length: n, Err: io.EOF}
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

	if _, err := p.readWithRetries(context.Background(), f, target{name: "disc.iso"}); err != nil {
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

	res, err := p.readWithRetries(context.Background(), f, target{name: "disc.iso"})
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

	_, err := p.readWithRetries(context.Background(), f, target{name: "disc.iso"})
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

// TestFetchLoopFetchesEveryAbsentPieceTheReadSpans is the Vision Pro .mov
// that hung the daemon. Its pieces are 256 KiB, and the MP4 reader asks for
// 2 MiB from byte 28: eight pieces, the first of which is already here. The
// loop used to fetch only the piece holding byte 28, which fetched nothing,
// and asked the prober the same question 700,000 times in two minutes.
func TestFetchLoopFetchesEveryAbsentPieceTheReadSpans(t *testing.T) {
	p := testPlugin(t, nil) // max_pieces 6: fewer than the read spans
	f := newFakeSampler(100<<20, 256<<10)
	sp := &scriptedProber{missing: []int64{28}, length: 2 << 20}
	p.probeImage = sp.image

	if _, err := p.readWithRetries(context.Background(), f, target{name: "disc.iso"}); err != nil {
		t.Fatalf("the loop should have fetched the whole read and succeeded: %v", err)
	}
	if sp.calls != 2 {
		t.Errorf("prober called %d times, want 2: one round fetches the whole read", sp.calls)
	}
	for i := 0; i <= f.PieceOf(28+2<<20-1); i++ {
		if !f.have[i] {
			t.Errorf("piece %d of the read was not fetched", i)
		}
	}
	// The ends, then pieces 1-8 in one round; piece 0 was already here.
	if want := 2 + 8; len(f.fetches) != want {
		t.Errorf("fetches = %v, want %d: nothing fetched twice", f.fetches, want)
	}
}

// A prober that names bytes which are all here already cannot be helped by
// fetching, so the loop must end rather than ask it again.
func TestFetchLoopEndsWhenTheNamedBytesArePresent(t *testing.T) {
	p := testPlugin(t, nil)
	f := newFakeSampler(100<<20, 16<<20)
	missing := make([]int64, 1000)
	for i := range missing {
		missing[i] = 28 // in piece 0, one of the ends
	}
	sp := &scriptedProber{missing: missing}
	p.probeImage = sp.image

	_, err := p.readWithRetries(context.Background(), f, target{name: "disc.iso"})
	if err == nil || !strings.Contains(err.Error(), "every piece holding them is here") {
		t.Fatalf("err = %v, want the loop to say fetching cannot help", err)
	}
	if sp.calls != 1 {
		t.Errorf("prober called %d times, want 1", sp.calls)
	}
}

// The loop stops when the probe's context ends, even when every fetch it
// makes returns at once.
func TestFetchLoopStopsWhenCancelled(t *testing.T) {
	p := testPlugin(t, nil)
	f := newFakeSampler(100<<20, 16<<20)
	sp := &scriptedProber{missing: []int64{3 << 24, 4 << 24}}
	p.probeImage = sp.image
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.readWithRetries(ctx, f, target{name: "disc.iso"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if sp.calls != 1 {
		t.Errorf("prober called %d times, want 1", sp.calls)
	}
}

// max_pieces is counted in 16 MiB pieces, which is what it was measured on,
// so a torrent with smaller pieces gets the same bytes rather than the same
// handful of pieces.
func TestPieceBudgetCountsSixteenMiBPieces(t *testing.T) {
	p := testPlugin(t, nil)
	for pieceLen, want := range map[int64]int{
		16 << 20:  6,
		32 << 20:  6,
		4 << 20:   24,
		256 << 10: 384,
		0:         6,
	} {
		if got := p.pieceBudget(pieceLen); got != want {
			t.Errorf("pieceBudget(%d) = %d, want %d", pieceLen, got, want)
		}
	}
}

// The client names a file it has not completed name+".part", which is every
// file a probe leaves behind; the cleanup has to find it under that name.
func TestRemoveSampleRemovesPartialFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"Disc.iso.part", "Folder/Film.mov.part", "Other.iso.part"} {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Disc.iso", "Folder", "Never.iso"} {
		if err := removeSample(dir, name); err != nil {
			t.Errorf("removeSample(%q) = %v", name, err)
		}
	}
	for _, gone := range []string{"Disc.iso.part", "Folder"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived the cleanup", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Other.iso.part")); err != nil {
		t.Errorf("another release's sample was removed: %v", err)
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

	if _, err := p.readWithRetries(context.Background(), f, target{name: "disc.iso"}); err == nil {
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

	if _, err := p.readWithRetries(context.Background(), f, target{name: "disc.iso"}); err == nil {
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

	res, err := p.readWithRetries(context.Background(), f, target{name: "Some.Film.2024.1080p.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != mvcprobe.KindMatroska || gotLabel != "Some.Film.2024.1080p.mkv" {
		t.Errorf("kind=%v label=%q", res.Kind, gotLabel)
	}
}

func TestMissingRange(t *testing.T) {
	if _, _, ok := missingRange(errors.New("plain"), 0); ok {
		t.Error("a plain error names no range")
	}
	// The prober's own error is preferred, since it knows which bytes the
	// format needed rather than which read happened to fail. Its offsets are
	// the file's, so the file's place in the torrent is added.
	md := &mvcprobe.MissingDataError{Offset: 4242, Length: 2048}
	if first, last, ok := missingRange(md, 1000); !ok || first != 5242 || last != 7289 {
		t.Errorf("probe error gave (%d-%d, %v), want (5242-7289, true)", first, last, ok)
	}
	mp := &torrentpeek.MissingPieceError{Offset: 99, Length: 10, Piece: 3}
	if first, last, ok := missingRange(mp, 1000); !ok || first != 99 || last != 108 {
		t.Errorf("reader error gave (%d-%d, %v), want (99-108, true)", first, last, ok)
	}
	// No length still names the one byte.
	if first, last, ok := missingRange(&torrentpeek.MissingPieceError{Offset: 99}, 0); !ok || first != 99 || last != 99 {
		t.Errorf("lengthless reader error gave (%d-%d, %v), want (99-99, true)", first, last, ok)
	}
	// Wrapped, as it arrives in practice: the prober's error wraps the
	// reader's, and the prober's wins.
	wrapped := &mvcprobe.MissingDataError{Offset: 4242, Length: 2048, Err: mp}
	if first, _, ok := missingRange(errors.Join(errors.New("ctx"), wrapped), 0); !ok || first != 4242 {
		t.Errorf("wrapped probe error gave (%d, %v)", first, ok)
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

func TestNewPluginDefaultsAndOverrides(t *testing.T) {
	pl, err := newPlugin(map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := pl.(*probePlugin)
	// Serial by default: the bandwidth cost of a successful probe is what the
	// serialisation protects a private tracker from.
	if p.parallel != 1 {
		t.Errorf("default parallel = %d, want 1", p.parallel)
	}
	if p.noPeerTimeout != defaultNoPeerTimeout {
		t.Errorf("default no_peer_timeout = %v, want %v", p.noPeerTimeout, defaultNoPeerTimeout)
	}

	pl, err = newPlugin(map[string]any{"parallel": float64(3), "no_peer_timeout": "5s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p = pl.(*probePlugin)
	if p.parallel != 3 {
		t.Errorf("parallel = %d, want 3", p.parallel)
	}
	if p.noPeerTimeout != 5*time.Second {
		t.Errorf("no_peer_timeout = %v, want 5s", p.noPeerTimeout)
	}

	// Zero disables the early give-up rather than meaning "immediately".
	pl, err = newPlugin(map[string]any{"no_peer_timeout": "0s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pl.(*probePlugin).noPeerTimeout; got != 0 {
		t.Errorf("no_peer_timeout 0s = %v, want 0", got)
	}
}

func TestValidateNewKeys(t *testing.T) {
	if errs := validate(map[string]any{"parallel": float64(2), "no_peer_timeout": "15s"}); len(errs) != 0 {
		t.Errorf("valid config rejected: %v", errs)
	}
	if errs := validate(map[string]any{"parallel": float64(0)}); len(errs) == 0 {
		t.Error("parallel below 1 should be rejected")
	}
	if errs := validate(map[string]any{"no_peer_timeout": "soon"}); len(errs) == 0 {
		t.Error("an unparseable no_peer_timeout should be rejected")
	}
	if errs := validate(map[string]any{"parallelism": float64(2)}); len(errs) == 0 {
		t.Error("an unknown key should be rejected")
	}
}

// Process must not build a torrent client — nor touch the network — when
// every entry has already been refused upstream.
func TestProcessSkipsWhenAllRejected(t *testing.T) {
	pl, err := newPlugin(map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := pl.(*probePlugin)

	e := entry.New("Some Release", "https://example.invalid/x.torrent")
	e.Reject("upstream said no")
	out, err := p.Process(context.Background(), &plugin.TaskContext{Logger: discardLogger()}, []*entry.Entry{e})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("a rejected entry should not be forwarded, got %d", len(out))
	}
	if p.sc != nil {
		t.Error("no torrent client should have been built")
	}
	if _, ok := e.Fields[entry.FieldProbeOK]; ok {
		t.Error("a rejected entry must not be stamped with probe fields")
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// --- the resolution claim, checked against the frame ---

func upscaleEntry(t *testing.T, title string, w, h int) *entry.Entry {
	t.Helper()
	e := entry.New(title, "http://x/t.torrent")
	e.SetQuality(quality.Parse(title))
	res := &mvcprobe.Result{
		Kind:   mvcprobe.KindMatroska,
		Layout: mvcprobe.Layout2D,
		Video:  []mvcprobe.VideoTrack{{Codec: "HEVC", Width: w, Height: h}},
	}
	apply(e, res)
	return e
}

// TestProbeUpscaleCaughtByTheFrame is what a bitrate floor could only infer: a
// release calling itself 2160p whose video is 1920 wide.
func TestProbeUpscaleCaughtByTheFrame(t *testing.T) {
	e := upscaleEntry(t, "Film.2026.2160p.BluRay.x265", 1920, 1080)
	up, ok := e.Fields[entry.FieldProbeUpscale].(bool)
	if !ok {
		t.Fatal("probe_upscale should be set when both sides are known")
	}
	if !up {
		t.Error("a 1920-wide frame does not back a 2160p claim")
	}
}

// TestProbeUpscaleFalseIsAnAnswer: the field is explicit in both directions,
// like probe_is_3d, so a condition can require a backed claim.
func TestProbeUpscaleFalseIsAnAnswer(t *testing.T) {
	e := upscaleEntry(t, "Film.2026.2160p.BluRay.x265", 3840, 2160)
	up, ok := e.Fields[entry.FieldProbeUpscale].(bool)
	if !ok {
		t.Fatal("probe_upscale should be set")
	}
	if up {
		t.Error("3840x2160 backs a 2160p claim")
	}
}

// TestProbeUpscaleJudgesWidthNotHeight: a scope film is 3840x1600, and a
// height test would read the most cinematic releases there are as upscales.
func TestProbeUpscaleJudgesWidthNotHeight(t *testing.T) {
	for _, h := range []int{1600, 2076, 2160} {
		e := upscaleEntry(t, "Film.2026.2160p.BluRay.x265", 3840, h)
		if e.Fields[entry.FieldProbeUpscale] != false {
			t.Errorf("3840x%d is a 2160p frame, got upscale=%v", h, e.Fields[entry.FieldProbeUpscale])
		}
	}
}

// TestProbeUpscaleSideBySideIsNotAnUpscale: an SBS frame is twice as wide as
// its claim, which is too big rather than too small.
func TestProbeUpscaleSideBySideIsNotAnUpscale(t *testing.T) {
	e := upscaleEntry(t, "Film.2026.1080p.3D.HSBS.BluRay.x264", 3840, 1080)
	if e.Fields[entry.FieldProbeUpscale] != false {
		t.Errorf("a 3840-wide SBS frame is not an upscale of a 1080p claim: %v",
			e.Fields[entry.FieldProbeUpscale])
	}
}

// TestProbeUpscaleAbsentWhenEitherSideIsUnknown: fields are absent rather
// than guessed, the rule the rest of this plugin follows.
func TestProbeUpscaleAbsentWhenEitherSideIsUnknown(t *testing.T) {
	// The container stated no frame size (a disc's dependent view).
	noFrame := upscaleEntry(t, "Film.2026.2160p.BluRay.x265", 0, 0)
	if _, ok := noFrame.Fields[entry.FieldProbeUpscale]; ok {
		t.Error("no frame size: the field must be absent")
	}
	// The name claims no resolution.
	noClaim := upscaleEntry(t, "Film.2026.BluRay.x265", 3840, 2160)
	if _, ok := noClaim.Fields[entry.FieldProbeUpscale]; ok {
		t.Error("no claimed resolution: the field must be absent")
	}
}

// TestProbeUpscaleTiersDoNotBleed: each threshold has to reject the tier below
// it while admitting its own, including a DCI frame.
func TestProbeUpscaleTiersDoNotBleed(t *testing.T) {
	for _, tc := range []struct {
		title string
		w     int
		want  bool
	}{
		{"Film.2026.2160p.x265", 4096, false}, // DCI 4K
		{"Film.2026.2160p.x265", 3840, false},
		{"Film.2026.2160p.x265", 1920, true},
		{"Film.2026.1080p.x265", 1920, false},
		{"Film.2026.1080p.x265", 1280, true},
		{"Film.2026.720p.x265", 1280, false},
		{"Film.2026.720p.x265", 720, true},
	} {
		e := upscaleEntry(t, tc.title, tc.w, 0)
		if got := e.Fields[entry.FieldProbeUpscale]; got != tc.want {
			t.Errorf("%s at %d wide: upscale = %v, want %v", tc.title, tc.w, got, tc.want)
		}
	}
}

// TestOnlyRefusesWhenRequired pins the reason this plugin carries a
// RefusalFor at all. Both probe nodes in a real MVC-harvest config leave
// require unset, so an unreadable release is passed on with probe_ok false
// and nothing is rejected here. Declaring the capability statically made the
// DAG ordering check emit two warnings naming a rejection the configured node
// cannot reach, next to two that were real — which is how a reader learns to
// skip the whole set.
func TestOnlyRefusesWhenRequired(t *testing.T) {
	d, ok := plugin.Lookup(pluginName)
	if !ok {
		t.Fatal("probe not registered")
	}
	if got := d.RefusalIn(map[string]any{}); got != plugin.RefusalNone {
		t.Errorf("require unset: RefusalIn = %v, want none", got)
	}
	if got := d.RefusalIn(map[string]any{"require": false}); got != plugin.RefusalNone {
		t.Errorf("require=false: RefusalIn = %v, want none", got)
	}
	if got := d.RefusalIn(map[string]any{"require": true}); got != plugin.RefusalPerRelease {
		t.Errorf("require=true: RefusalIn = %v, want per-release", got)
	}
}

// TestTargetIsTheLargestFile: a release packaged as a folder is probed by its
// main file, found by size, at its place in the torrent. The torrent's own
// name says nothing about the reader a folder needs.
func TestTargetIsTheLargestFile(t *testing.T) {
	info := &metainfo.Info{Name: "Hugo 3D AVP MV HEVC", Files: []metainfo.FileInfo{
		{Path: []string{"Hugo.nfo"}, Length: 2 << 10},
		{Path: []string{"Sample", "sample.mov"}, Length: 30 << 20},
		{Path: []string{"Hugo.mov"}, Length: 9 << 30},
		{Path: []string{"Hugo.srt"}, Length: 80 << 10},
	}}
	got := targetIn(info, "unused")
	want := target{name: "Hugo.mov", off: 2<<10 + 30<<20, size: 9 << 30}
	if got != want {
		t.Errorf("target %+v, want %+v", got, want)
	}
	if readerFor(got.name) != "mp4" {
		t.Errorf("reader %q for %q, want mp4", readerFor(got.name), got.name)
	}

	single := targetIn(&metainfo.Info{Name: "Some.Film.2024.iso", Length: 40 << 30}, "Some.Film.2024.iso")
	if single != (target{name: "Some.Film.2024.iso", size: 40 << 30}) {
		t.Errorf("single-file target %+v", single)
	}
}

func TestReaderForExtension(t *testing.T) {
	for name, want := range map[string]string{
		"a.mkv": "matroska", "a.MK3D": "matroska", "a.webm": "matroska",
		"a.mp4": "mp4", "a.MOV": "mp4", "a.m4v": "mp4",
		"a.iso": "image", "Some Film 2012 3D BluRay": "image", "": "image",
	} {
		if got := readerFor(name); got != want {
			t.Errorf("readerFor(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestMP4InAFolderReadsTheFileAtItsOffset: the MP4 prober reads the target
// file, not the torrent, and its "missing" offsets count from the file's
// start. The piece fetched for one must be the torrent's piece holding
// file offset + where the file starts -- reading it as a torrent offset
// fetches a piece from the wrong file and the probe never finishes.
func TestMP4InAFolderReadsTheFileAtItsOffset(t *testing.T) {
	const piece = 16 << 20
	f := newFakeSampler(100<<20, piece)
	for i := range f.data {
		f.data[i] = byte(i / piece) // each piece's bytes name it
	}
	tg := target{name: "Film/Film.mov", off: 20 << 20, size: 70 << 20}

	p := testPlugin(t, nil)
	p.probeImage = func(io.ReaderAt, int64, string) (*mvcprobe.Result, error) {
		t.Error("an .mov must not be probed as a disc image")
		return nil, errors.New("wrong path")
	}
	calls := 0
	p.probeMP4 = func(r io.ReaderAt, size int64, label string) (*mvcprobe.Result, error) {
		calls++
		if size != tg.size || label != tg.name {
			t.Errorf("prober given size %d label %q, want the file's", size, label)
		}
		if calls == 1 {
			// The movie box, as the prober would ask for it: 40 MiB into the file.
			return nil, &mvcprobe.MissingDataError{Offset: 40 << 20, Length: 8, Err: io.EOF}
		}
		var b [1]byte
		if _, err := r.ReadAt(b[:], 40<<20); err != nil {
			return nil, err
		}
		if want := byte((20<<20 + 40<<20) / piece); b[0] != want {
			t.Errorf("file offset 40 MiB read torrent piece %d, want %d", b[0], want)
		}
		return &mvcprobe.Result{Kind: mvcprobe.KindMP4, Is3D: true, Layout: mvcprobe.LayoutMVHEVC}, nil
	}

	res, err := p.readWithRetries(context.Background(), f, tg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Layout != mvcprobe.LayoutMVHEVC {
		t.Errorf("layout %v", res.Layout)
	}
	// First the file's two ends, then the piece the prober asked for.
	wantFetches := []int{20 << 20 / piece, (90<<20 - 1) / piece, (60 << 20) / piece}
	if fmt.Sprint(f.fetches) != fmt.Sprint(wantFetches) {
		t.Errorf("fetched pieces %v, want %v", f.fetches, wantFetches)
	}
}

// TestLayoutNames pins probe_3d_layout's vocabulary, and that a layout this
// build has never heard of is recorded as 3D ("other"), never as "2d".
func TestLayoutNames(t *testing.T) {
	for l, want := range map[mvcprobe.Layout]string{
		mvcprobe.Layout2D:         "2d",
		mvcprobe.LayoutMVC:        "mvc",
		mvcprobe.LayoutMVHEVC:     "mv-hevc",
		mvcprobe.LayoutSideBySide: "sbs",
		mvcprobe.LayoutTopBottom:  "tab",
		mvcprobe.LayoutOther:      "other",
		mvcprobe.Layout(99):       "other",
	} {
		if got := layoutName(l); got != want {
			t.Errorf("layoutName(%d) = %q, want %q", l, got, want)
		}
	}
	if kindName(mvcprobe.KindMP4) != "mp4" {
		t.Errorf("kindName(KindMP4) = %q", kindName(mvcprobe.KindMP4))
	}
	// The name parser and the probe share the word for MV-HEVC, so a rule
	// can compare what a release claims against what it is.
	if got := quality.Parse("Hugo 3D AVP MV HEVC mov").Format3D.Layout(); got != layoutName(mvcprobe.LayoutMVHEVC) {
		t.Errorf("name says %q, probe says %q", got, layoutName(mvcprobe.LayoutMVHEVC))
	}
}

// TestARealMVHEVCFileInAFolder runs the real prober over a real file: a
// 640x368 MV-HEVC QuickTime made by bdtools' own muxer (the movie box
// last, as its muxer writes it), placed partway into a torrent beside other
// files. Fetching starts from the file's ends and the prober's missing
// offsets are the file's, so this exercises everything between the torrent
// and the verdict that the fakes above stand in for.
func TestARealMVHEVCFileInAFolder(t *testing.T) {
	mov, err := os.ReadFile(filepath.Join("testdata", "mvhevc.mov"))
	if err != nil {
		t.Fatal(err)
	}
	const piece = 16 << 10
	const before = 50 << 10 // an .nfo and a sample ahead of the film
	f := newFakeSampler(int64(before+len(mov)+30<<10), piece)
	copy(f.data[before:], mov)

	p := testPlugin(t, nil)
	tg := target{name: "Film.mov", off: before, size: int64(len(mov))}
	res, err := p.readWithRetries(context.Background(), f, tg)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("Film 3D AVP MV HEVC", "http://x/film")
	apply(e, res)
	for field, want := range map[string]any{
		entry.FieldProbeOK:       true,
		entry.FieldProbeKind:     "mp4",
		entry.FieldProbeIs3D:     true,
		entry.FieldProbe3DLayout: "mv-hevc",
		entry.FieldProbeWidth:    640,
		entry.FieldProbeHeight:   368,
	} {
		if got := e.Fields[field]; got != want {
			t.Errorf("%s = %#v, want %#v", field, got, want)
		}
	}
	// The film starts in piece 3 and ends in piece 9; its movie box is at
	// the end. Nothing in between is needed.
	if f.FetchedPieces() > 3 {
		t.Errorf("fetched %d pieces %v for a file whose index is in its last piece", f.FetchedPieces(), f.fetches)
	}
}

// TestAProvenFormatRaisesTheQuality: a disc named only "3D BluRay" reads as
// Unspecified, and once the probe has read its MVC view it must compare as the
// BD3D it is -- otherwise the final dedup in 3d-mvc keeps a Vision Pro copy of
// the film over the disc. A name that claimed more, or said "conversion",
// keeps what it said.
func TestAProvenFormatRaisesTheQuality(t *testing.T) {
	tests := []struct {
		name   string
		layout mvcprobe.Layout
		want   quality.Format3D
	}{
		{"Avatar 2009 3D BluRay 1080p AVC", mvcprobe.LayoutMVC, quality.Format3DBD},
		{"Avatar 2009 3D HSBS 1080p", mvcprobe.LayoutMVC, quality.Format3DBD},
		{"Hugo 3D 2011 mov", mvcprobe.LayoutMVHEVC, quality.Format3DMVHEVC},
		// Already at least what the probe proved: unchanged.
		{"Avatar 2009 BD3D 1080p", mvcprobe.LayoutMVC, quality.Format3DBD},
		{"Avatar 2009 BD3D 1080p", mvcprobe.LayoutMVHEVC, quality.Format3DBD},
		// The probe reads the format, not the provenance.
		{"AVPR Aliens vs Predator Requiem 2007 3D Convert MVC", mvcprobe.LayoutMVC, quality.Format3DConv},
		// A layout that proves no stereo format changes nothing.
		{"Avatar 2009 3D BluRay 1080p AVC", mvcprobe.Layout2D, quality.Format3DUnspecified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := entry.New(tt.name, "http://x/1")
			e.SetQuality(quality.Parse(tt.name))
			apply(e, &mvcprobe.Result{Kind: mvcprobe.KindDisc, Is3D: true, Layout: tt.layout})
			q, _ := e.Quality()
			if q.Format3D != tt.want {
				t.Errorf("format = %v, want %v", q.Format3D, tt.want)
			}
		})
	}
}
