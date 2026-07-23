package torrent_failed

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

var now = time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "janitor",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newTestPlugin(t *testing.T, cfg map[string]any) *failedPlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	fp := p.(*failedPlugin)
	fp.now = func() time.Time { return now }
	return fp
}

func sessionEntry(state string, fields map[string]any) *entry.Entry {
	e := entry.New("Some.Torrent", "torrent://aaaa")
	e.Set(entry.FieldTorrentState, state)
	for k, v := range fields {
		e.Set(k, v)
	}
	return e
}

func classify(t *testing.T, p *failedPlugin, e *entry.Entry) {
	t.Helper()
	if _, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
}

func TestErroredAccepted(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("errored", map[string]any{
		entry.FieldTorrentError: "tracker unregistered",
	})
	classify(t, p, e)
	if !e.IsAccepted() {
		t.Fatalf("errored torrent should be accepted, state=%v reason=%q", e.State, e.RejectReason)
	}
	if !strings.Contains(e.AcceptReason, "tracker unregistered") {
		t.Errorf("accept reason = %q", e.AcceptReason)
	}
}

func TestErroredWithoutMessageAccepted(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("errored", nil)
	classify(t, p, e)
	if !e.IsAccepted() {
		t.Fatal("errored torrent without message should still be accepted")
	}
}

func TestStalledPastTimeoutAccepted(t *testing.T) {
	p := newTestPlugin(t, nil) // default 4h
	e := sessionEntry("stalled", map[string]any{
		entry.FieldTorrentLastActivity: now.Add(-5 * time.Hour),
		entry.FieldTorrentAddedAt:      now.Add(-48 * time.Hour),
	})
	classify(t, p, e)
	if !e.IsAccepted() {
		t.Fatalf("stalled past timeout should be accepted, reason=%q", e.RejectReason)
	}
}

func TestStalledWithinTimeoutRejected(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("stalled", map[string]any{
		entry.FieldTorrentLastActivity: now.Add(-30 * time.Minute),
	})
	classify(t, p, e)
	if !e.IsRejected() {
		t.Fatal("recently-active stalled torrent should be rejected (not yet failed)")
	}
}

// A fresh torrent with zero progress and no activity yet is judged from its
// add time: too fresh → rejected.
func TestFreshZeroProgressRejected(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("downloading", map[string]any{
		entry.FieldTorrentProgress: 0.0,
		entry.FieldTorrentAddedAt:  now.Add(-10 * time.Minute),
	})
	classify(t, p, e)
	if !e.IsRejected() {
		t.Fatal("fresh zero-progress download should be rejected")
	}
}

func TestZeroProgressPastTimeoutAccepted(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("downloading", map[string]any{
		entry.FieldTorrentProgress: 0.0,
		entry.FieldTorrentAddedAt:  now.Add(-6 * time.Hour),
	})
	classify(t, p, e)
	if !e.IsAccepted() {
		t.Fatalf("zero-progress download past timeout should be accepted, reason=%q", e.RejectReason)
	}
}

// Slow but moving: downloading with progress > 0 is healthy no matter how old.
func TestSlowDownloadRejected(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("downloading", map[string]any{
		entry.FieldTorrentProgress: 12.5,
		entry.FieldTorrentAddedAt:  now.Add(-72 * time.Hour),
	})
	classify(t, p, e)
	if !e.IsRejected() {
		t.Fatal("slow-but-progressing download should be rejected")
	}
}

// Stalled but with recent activity fallback: activity refreshes the clock.
func TestStalledRecentActivityWinsOverOldAddTime(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("stalled", map[string]any{
		entry.FieldTorrentLastActivity: now.Add(-1 * time.Hour),
		entry.FieldTorrentAddedAt:      now.Add(-240 * time.Hour),
	})
	classify(t, p, e)
	if !e.IsRejected() {
		t.Fatal("stalled torrent with recent activity should be rejected")
	}
}

func TestHealthyStatesRejected(t *testing.T) {
	p := newTestPlugin(t, nil)
	for _, state := range []string{"seeding", "paused", "checking"} {
		e := sessionEntry(state, map[string]any{
			entry.FieldTorrentAddedAt: now.Add(-100 * time.Hour),
		})
		classify(t, p, e)
		if !e.IsRejected() {
			t.Errorf("state %s should be rejected", state)
		}
	}
}

func TestStalledWithoutTimestampsRejected(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("stalled", nil)
	classify(t, p, e)
	if !e.IsRejected() {
		t.Fatal("stalled torrent without timestamps should be kept (rejected), not failed")
	}
}

func TestCustomStallTimeout(t *testing.T) {
	p := newTestPlugin(t, map[string]any{"stall_timeout": "30m"})
	e := sessionEntry("stalled", map[string]any{
		entry.FieldTorrentLastActivity: now.Add(-45 * time.Minute),
	})
	classify(t, p, e)
	if !e.IsAccepted() {
		t.Fatal("45m inactivity should exceed a 30m stall_timeout")
	}
}

func TestInvalidStallTimeout(t *testing.T) {
	if _, err := newPlugin(map[string]any{"stall_timeout": "banana"}, nil); err == nil {
		t.Fatal("invalid stall_timeout should error")
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{"stall_timeout": "4h"}); len(errs) != 0 {
		t.Errorf("valid config produced errors: %v", errs)
	}
	if errs := validate(map[string]any{"stall_timeout": "nope"}); len(errs) == 0 {
		t.Error("invalid duration should error")
	}
	if errs := validate(map[string]any{"bogus": true}); len(errs) == 0 {
		t.Error("unknown key should error")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup(pluginName)
	if !ok {
		t.Fatal("torrent_failed not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("role = %v", d.Role)
	}
}
