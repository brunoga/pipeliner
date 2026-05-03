package premiere

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makePlugin(t *testing.T, cfg map[string]any) *premierePlugin {
	t.Helper()
	if _, ok := cfg["db"]; !ok {
		cfg["db"] = ":memory:"
	}
	p, err := newPlugin(cfg)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*premierePlugin)
}

func makeEntry(seriesName string, season, episode int) *entry.Entry {
	e := entry.New(seriesName+".S01E01.720p", "http://example.com/"+seriesName+".torrent")
	e.Set("series_name", seriesName)
	e.Set("series_season", season)
	e.Set("series_episode", episode)
	return e
}

func filter(t *testing.T, p *premierePlugin, e *entry.Entry) {
	t.Helper()
	tc := &plugin.TaskContext{Name: "test-task"}
	if err := p.Filter(context.Background(), tc, e); err != nil {
		t.Fatalf("Filter: %v", err)
	}
}

func TestNewSeriesAccepted(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	e := makeEntry("Breaking Bad", 1, 1)
	filter(t, p, e)
	if !e.IsAccepted() {
		t.Errorf("S01E01 of new series should be accepted; reason: %q", e.RejectReason)
	}
}

func TestNonPremiereEpisodeRejected(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	e := makeEntry("Breaking Bad", 1, 2)
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("S01E02 should be rejected (not premiere)")
	}
}

func TestWrongSeasonRejected(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	e := makeEntry("Breaking Bad", 2, 1)
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("S02E01 should be rejected (season != 1)")
	}
}

func TestAnySeasonMode(t *testing.T) {
	p := makePlugin(t, map[string]any{"season": 0})
	e := makeEntry("Breaking Bad", 2, 1)
	filter(t, p, e)
	if !e.IsAccepted() {
		t.Errorf("S02E01 should be accepted when season=0; reason: %q", e.RejectReason)
	}
}

func TestAlreadySeenRejected(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	tc := &plugin.TaskContext{Name: "test-task"}

	// First run — accept.
	e1 := makeEntry("Breaking Bad", 1, 1)
	if err := p.Filter(context.Background(), tc, e1); err != nil {
		t.Fatal(err)
	}
	if !e1.IsAccepted() {
		t.Fatal("first run should accept")
	}

	// Second run — same series, should be rejected.
	e2 := makeEntry("Breaking Bad", 1, 1)
	if err := p.Filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsRejected() {
		t.Error("second run should reject already-seen premiere")
	}
}

func TestNoSeriesNameRejected(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	e := entry.New("random.file.mkv", "http://example.com/file.mkv")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("entry without series_name should be rejected")
	}
}

func TestDifferentSeriesIndependent(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	tc := &plugin.TaskContext{Name: "test-task"}

	e1 := makeEntry("Breaking Bad", 1, 1)
	_ = p.Filter(context.Background(), tc, e1)

	// Different series — should also be accepted.
	e2 := makeEntry("The Wire", 1, 1)
	_ = p.Filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("premiere of different series should be accepted; reason: %q", e2.RejectReason)
	}
}
