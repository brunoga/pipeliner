package bitrate

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
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
	if got := e.Fields[entry.FieldVideoBitrateMbps]; got != 12.8 {
		t.Errorf("video_bitrate_mbps = %v, want 12.8", got)
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
	if e.Fields[entry.FieldVideoBitrateMbps] != 12.8 {
		t.Errorf("field should still be stamped: %v", e.Fields[entry.FieldVideoBitrateMbps])
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
