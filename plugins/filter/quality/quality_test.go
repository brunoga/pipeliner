package quality

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func TestAcceptsMatchingQuality(t *testing.T) {
	p, err := newPlugin(map[string]any{"min": "720p", "max": "1080p"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("Show.S01E01.1080p.BluRay.x264", "http://x.com/a")
	p.(*qualityPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsRejected() {
		t.Errorf("1080p should be accepted within 720p-1080p: %s", e.RejectReason)
	}
}

func TestRejectsBelowMin(t *testing.T) {
	p, _ := newPlugin(map[string]any{"min": "720p", "max": "1080p"}, nil)
	e := entry.New("Show.S01E01.480p.HDTV", "http://x.com/a")
	p.(*qualityPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("480p should be rejected when min is 720p")
	}
}

func TestRejectsAboveMax(t *testing.T) {
	p, _ := newPlugin(map[string]any{"min": "720p", "max": "1080p"}, nil)
	e := entry.New("Show.S01E01.2160p.BluRay.x265", "http://x.com/a")
	p.(*qualityPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("2160p should be rejected when max is 1080p")
	}
}

func TestMinOnly(t *testing.T) {
	p, err := newPlugin(map[string]any{"min": "720p"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hi := entry.New("Show.S01E01.2160p.BluRay", "http://x.com/a")
	p.(*qualityPlugin).Filter(context.Background(), makeCtx(), hi) //nolint:errcheck
	if hi.IsRejected() {
		t.Error("2160p should pass when only min=720p is set")
	}

	lo := entry.New("Show.S01E01.480p.HDTV", "http://x.com/b")
	p.(*qualityPlugin).Filter(context.Background(), makeCtx(), lo) //nolint:errcheck
	if !lo.IsRejected() {
		t.Error("480p should be rejected when min=720p")
	}
}

func TestMaxOnly(t *testing.T) {
	p, err := newPlugin(map[string]any{"max": "1080p"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("Show.S01E01.2160p.BluRay", "http://x.com/a")
	p.(*qualityPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("2160p should be rejected when max=1080p")
	}
}

func TestQualityFieldSet(t *testing.T) {
	p, _ := newPlugin(map[string]any{"min": "720p"}, nil)
	e := entry.New("Show.S01E01.1080p.BluRay.x264", "http://x.com/a")
	p.(*qualityPlugin).Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if v := e.GetString("quality"); v == "" {
		t.Error("quality field should be set after filtering")
	}
}

func TestMissingBothBounds(t *testing.T) {
	if _, err := newPlugin(map[string]any{}, nil); err == nil {
		t.Error("expected error when neither min nor max is set")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("quality")
	if !ok {
		t.Fatal("quality plugin not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("phase: got %v", d.Role)
	}
}
