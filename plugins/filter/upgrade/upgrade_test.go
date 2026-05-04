package upgrade

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makePlugin(t *testing.T, cfg map[string]any) *upgradePlugin {
	t.Helper()
	if _, ok := cfg["db"]; !ok {
		cfg["db"] = ":memory:"
	}
	if _, ok := cfg["target"]; !ok {
		cfg["target"] = "2160p"
	}
	p, err := newPlugin(cfg)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*upgradePlugin)
}

func filterLearn(t *testing.T, p *upgradePlugin, e *entry.Entry) {
	t.Helper()
	tc := &plugin.TaskContext{Name: "test-task"}
	if err := p.Filter(context.Background(), tc, e); err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if e.IsAccepted() {
		if err := p.Learn(context.Background(), tc, []*entry.Entry{e}); err != nil {
			t.Fatalf("Learn: %v", err)
		}
	}
}

func entryWithQuality(title string) *entry.Entry {
	e := entry.New(title, "http://example.com/"+title)
	// Set series metadata so entryKey produces a stable key regardless of quality in title.
	e.Set("series_name", "Show")
	e.Set("series_id", "S01E01")
	return e
}

func TestFirstDownloadAccepted(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	e := entryWithQuality("Show.S01E01.720p.HDTV")
	filterLearn(t, p, e)
	if !e.IsAccepted() {
		t.Errorf("first download should be accepted; reason: %q", e.RejectReason)
	}
}

func TestUpgradeAccepted(t *testing.T) {
	p := makePlugin(t, map[string]any{})

	// First: 720p
	e1 := entryWithQuality("Show.S01E01.720p.HDTV")
	filterLearn(t, p, e1)

	// Then: 1080p — should be accepted as upgrade.
	e2 := entryWithQuality("Show.S01E01.1080p.BluRay")
	e2.Set("quality", "1080p")
	filterLearn(t, p, e2)
	if !e2.IsAccepted() {
		t.Errorf("1080p should upgrade from 720p; reason: %q", e2.RejectReason)
	}
}

func TestLowerQualityRejected(t *testing.T) {
	p := makePlugin(t, map[string]any{"on_lower": "reject"})

	// First: 1080p
	e1 := entryWithQuality("Show.S01E01.1080p.BluRay")
	filterLearn(t, p, e1)

	// Then: 720p — should be rejected.
	e2 := entryWithQuality("Show.S01E01.720p.HDTV")
	filterLearn(t, p, e2)
	if !e2.IsRejected() {
		t.Error("720p should be rejected when stored is 1080p and on_lower=reject")
	}
}

func TestAtTargetRejected(t *testing.T) {
	p := makePlugin(t, map[string]any{"target": "1080p"})

	// Download 1080p (at target).
	e1 := entryWithQuality("Show.S01E01.1080p.BluRay")
	e1.Set("quality", "1080p")
	filterLearn(t, p, e1)

	// Try again with 1080p — should be rejected (ceiling reached).
	e2 := entryWithQuality("Show.S01E01.1080p.WEB-DL")
	e2.Set("quality", "1080p")
	filterLearn(t, p, e2)
	if !e2.IsRejected() {
		t.Error("should be rejected when already at target quality")
	}
}

func TestOnLowerAccept(t *testing.T) {
	p := makePlugin(t, map[string]any{"on_lower": "accept"})

	// First: 1080p
	e1 := entryWithQuality("Show.S01E01.1080p.BluRay")
	filterLearn(t, p, e1)

	// Then: 720p with on_lower=accept — should be explicitly accepted.
	e2 := entryWithQuality("Show.S01E01.720p.HDTV")
	filterLearn(t, p, e2)
	if !e2.IsAccepted() {
		t.Errorf("720p should be accepted when on_lower=accept; state is %v", e2.State)
	}
}

func TestMissingTarget(t *testing.T) {
	_, err := newPlugin(map[string]any{"db": ":memory:"})
	if err == nil {
		t.Error("expected error when target is missing")
	}
}

func TestInvalidOnLower(t *testing.T) {
	_, err := newPlugin(map[string]any{"db": ":memory:", "target": "1080p", "on_lower": "maybe"})
	if err == nil {
		t.Error("expected error for invalid on_lower value")
	}
}
