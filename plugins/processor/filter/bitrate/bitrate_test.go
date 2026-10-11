package bitrate

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/quality"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func mk(t *testing.T, cfg map[string]any) *bitratePlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*bitratePlugin)
}

// ent builds an entry with size (bytes), runtime (min), and a parsed quality.
func ent(title string, size int64, runtimeMin int) *entry.Entry {
	e := entry.New(title, "http://x/"+title)
	if size > 0 {
		e.Set(entry.FieldTorrentFileSize, size)
	}
	if runtimeMin > 0 {
		e.Set(entry.FieldVideoRuntime, runtimeMin)
	}
	e.SetQuality(quality.Parse(title))
	return e
}

// The motivating case: the Supergirl 3D conversion — 10.35 GB over 108 min
// ≈ 12.8 Mbps for a 2160p frame — must be rejected by a 15 Mbps floor, and
// the computed rate stamped on the entry either way.
func TestStarvedEncodeRejected(t *testing.T) {
	p := mk(t, map[string]any{"min_2160p": 15})
	e := ent("Supergirl.2026.2160p.fsbs.x264", 10_353_197_344, 108)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Fatalf("12.8 Mbps 2160p should be rejected under a 15 Mbps floor")
	}
	// The file is 12.8 Mbps; the video is that minus the audio, which an
	// x264 release naming no audio format contributes 0.3 of by estimate.
	if got := e.Fields[entry.FieldContainerBitrateMbps]; got != 12.8 {
		t.Errorf("container_bitrate_mbps = %v, want 12.8", got)
	}
	if got := e.Fields[entry.FieldVideoBitrateMbps]; got != 12.5 {
		t.Errorf("video_bitrate_mbps = %v, want 12.5", got)
	}
}

func TestHealthyEncodePasses(t *testing.T) {
	p := mk(t, map[string]any{"min_2160p": 15, "min_1080p": 6})
	// 25 GB over 108 min ≈ 30.9 Mbps 2160p → passes.
	e := ent("Movie.2026.2160p.BluRay.x265", 25_000_000_000, 108)
	// 4 GB over 108 min ≈ 4.9 Mbps 1080p → rejected under 6.
	e2 := ent("Movie.2026.1080p.WEB-DL.x264", 4_000_000_000, 108)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e, e2}); err != nil {
		t.Fatal(err)
	}
	if e.IsRejected() {
		t.Errorf("healthy 2160p rejected: %s", e.RejectReason)
	}
	if !e2.IsRejected() {
		t.Error("starved 1080p should be rejected")
	}
}

func TestAbsentDataNeverRejects(t *testing.T) {
	p := mk(t, map[string]any{"min_2160p": 15, "min_other": 5})
	noSize := ent("Movie.2026.2160p.x265", 0, 108)
	noRuntime := ent("Movie.2026.2160p.x265", 10_000_000_000, 0)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{noSize, noRuntime}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []*entry.Entry{noSize, noRuntime} {
		if e.IsRejected() {
			t.Errorf("absent data must never reject: %s", e.RejectReason)
		}
		if _, ok := e.Fields[entry.FieldVideoBitrateMbps]; ok {
			t.Error("bitrate field must stay unset without both inputs")
		}
	}
}

func TestNoFloorStampsOnly(t *testing.T) {
	p := mk(t, nil)
	e := ent("Movie.2026.2160p.x264", 10_353_197_344, 108)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if e.IsRejected() {
		t.Error("no floors configured → never rejects")
	}
	if e.Fields[entry.FieldContainerBitrateMbps] != 12.8 {
		t.Errorf("container figure should still be stamped: %v", e.Fields[entry.FieldContainerBitrateMbps])
	}
	if e.Fields[entry.FieldVideoBitrateMbps] != 12.5 {
		t.Errorf("video figure should still be stamped: %v", e.Fields[entry.FieldVideoBitrateMbps])
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{"min_2160p": 15, "min_1080p": 6}); len(errs) != 0 {
		t.Errorf("valid config: %v", errs)
	}
	if errs := validate(map[string]any{"min_2160p": -1}); len(errs) == 0 {
		t.Error("negative floor must fail")
	}
	if errs := validate(map[string]any{"bogus": 1}); len(errs) == 0 {
		t.Error("unknown key must fail")
	}
}

// --- the video is not the file ---

// TestAudioIsNotVideo measures the case this was found on: a real 4K encode,
// 7.12 GB over 108 minutes, carrying TrueHD Atmos 7.1 plus an AC3
// compatibility track. The file is 8.8 Mbps; the video is 5.1. Judging the
// container figure made 42% of the number audio.
func TestAudioIsNotVideo(t *testing.T) {
	p := mk(t, nil)
	e := ent("Supergirl.2026.2160p.DV.HDR10.HEVC.10bit.TrueHD.Atmos.7.1", 7_122_905_630, 108)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	container, _ := e.Fields[entry.FieldContainerBitrateMbps].(float64)
	video, _ := e.Fields[entry.FieldVideoBitrateMbps].(float64)
	if container != 8.8 {
		t.Errorf("container_bitrate_mbps = %v, want 8.8", container)
	}
	// 8.8 less the 3.5 an Atmos-on-TrueHD bed is estimated at. The file's
	// true video rate is 5.12, so the estimate lands within 0.2 Mbps.
	if video != 5.3 {
		t.Errorf("video_bitrate_mbps = %v, want 5.3", video)
	}
	if video >= container {
		t.Error("the video cannot be the whole file")
	}
}

// TestLosslessAudioNoLongerFlattersARelease: two releases with the same video
// and different audio used to differ by 3 Mbps in the figure the floor judged.
func TestLosslessAudioNoLongerFlattersARelease(t *testing.T) {
	p := mk(t, nil)
	// Same video bitrate, 2.8 Mbps apart in audio.
	lossless := ent("Film.2026.2160p.BluRay.x265.TrueHD.Atmos", 7_000_000_000, 108)
	lossy := ent("Film.2026.2160p.BluRay.x265.DDP5.1.Atmos", 7_000_000_000, 108)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{lossless, lossy}); err != nil {
		t.Fatal(err)
	}
	lv, _ := lossless.Fields[entry.FieldVideoBitrateMbps].(float64)
	yv, _ := lossy.Fields[entry.FieldVideoBitrateMbps].(float64)
	if !(yv > lv) {
		t.Errorf("the lossy-audio release has more room for video: lossless=%v lossy=%v", lv, yv)
	}
	if d := yv - lv; d < 2.5 || d > 3.0 {
		t.Errorf("difference = %v Mbps, want the audio gap (~2.8)", d)
	}
}

// TestUnknownAudioErrsTowardsLenient: the subtraction is an estimate, and an
// over-estimate would refuse a release over a guess.
func TestUnknownAudioErrsTowardsLenient(t *testing.T) {
	p := mk(t, nil)
	e := ent("Film.2026.2160p.BluRay.x265", 7_000_000_000, 108)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	container, _ := e.Fields[entry.FieldContainerBitrateMbps].(float64)
	video, _ := e.Fields[entry.FieldVideoBitrateMbps].(float64)
	if container-video > 0.5 {
		t.Errorf("unknown audio subtracted %.1f Mbps; want a token amount", container-video)
	}
}

// --- codec-scaled floors ---

// TestFloorScalesWithCodec is the false rejection that started this: a 15 Mbps
// floor for 2160p refused HBO Max WEB-DLs at 7-10 Mbps, which are copies of
// what the service sent rather than encodes anyone chose the bitrate of.
func TestFloorScalesWithCodec(t *testing.T) {
	// 108 min, 7 GB → 8.6 Mbps container, ~7.9 video with DDP 5.1.
	const size, mins = 7_000_000_000, 108
	for _, tc := range []struct {
		name   string
		title  string
		floor  int
		reject bool
	}{
		// H.264 at this rate for 4K really is starved: full floor applies.
		{"h264 holds the full floor", "Film.2026.2160p.WEB-DL.H.264.DDP5.1", 15, true},
		// HEVC needs ~60% of it, so 15 means 9 — still above 7.9.
		{"hevc scaled still rejects at 15", "Film.2026.2160p.WEB-DL.H.265.DDP5.1", 15, true},
		// A recalibrated floor passes the same release.
		{"hevc passes a 10 floor", "Film.2026.2160p.WEB-DL.H.265.DDP5.1", 10, false},
		// AV1 needs less again: 15 means 6.75.
		{"av1 passes a 12 floor", "Film.2026.2160p.WEB-DL.AV1.DDP5.1", 12, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := mk(t, map[string]any{"min_2160p": tc.floor})
			e := ent(tc.title, size, mins)
			if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
				t.Fatal(err)
			}
			if got := e.IsRejected(); got != tc.reject {
				t.Errorf("rejected = %v, want %v (video %v, reason %q)",
					got, tc.reject, e.Fields[entry.FieldVideoBitrateMbps], e.RejectReason)
			}
		})
	}
}

// TestScaledReasonNamesTheArithmetic: a release refused by a number that is
// not the configured one has to say where that number came from.
func TestScaledReasonNamesTheArithmetic(t *testing.T) {
	p := mk(t, map[string]any{"min_2160p": 15})
	e := ent("Film.2026.2160p.WEB-DL.H.265.DDP5.1", 7_000_000_000, 108)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"9.0 Mbps", "15 Mbps scaled by 0.60", "H.265"} {
		if !strings.Contains(e.RejectReason, want) {
			t.Errorf("reason %q should mention %q", e.RejectReason, want)
		}
	}
}

// --- runtime source ---

// TestProbeDurationPreferred: a provider's runtime describes the film, the
// container's duration describes the file, and for an extended cut they are
// different films.
func TestProbeDurationPreferred(t *testing.T) {
	p := mk(t, nil)
	e := ent("Film.2026.2160p.BluRay.x265.DDP5.1", 7_000_000_000, 108)
	// The container says 150 minutes, not 108.
	e.Set(entry.FieldProbeDurationSec, 150*60)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if got := e.Fields[entry.FieldContainerBitrateMbps]; got != 6.2 {
		t.Errorf("container_bitrate_mbps = %v, want 6.2 (7 GB over 150 min)", got)
	}
}

// TestRuntimeFromProbeAlone: an entry with no enrichment runtime is still
// measurable when a probe ran above this node.
func TestRuntimeFromProbeAlone(t *testing.T) {
	p := mk(t, map[string]any{"min_2160p": 15})
	e := ent("Film.2026.2160p.WEB-DL.H.264.DDP5.1", 7_000_000_000, 0)
	e.Set(entry.FieldProbeDurationSec, 108*60)
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if e.Fields[entry.FieldContainerBitrateMbps] == nil {
		t.Fatal("probe duration alone should be enough to measure")
	}
	if !e.IsRejected() {
		t.Error("7.9 Mbps of H.264 video at 2160p is below a 15 Mbps floor")
	}
}
