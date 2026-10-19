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
	"github.com/brunoga/pipeliner/internal/store"
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

// Slow but moving: recent activity keeps a download healthy no matter how long
// ago it was added — the inactivity clock is driven by last-activity, not age.
func TestSlowDownloadRejected(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("downloading", map[string]any{
		entry.FieldTorrentProgress:     12.5,
		entry.FieldTorrentLastActivity: now.Add(-20 * time.Minute),
		entry.FieldTorrentAddedAt:      now.Add(-72 * time.Hour),
	})
	classify(t, p, e)
	if !e.IsRejected() {
		t.Fatal("slow-but-moving download (recent activity) should be rejected")
	}
}

// A partially-downloaded torrent that then stalls — the case that previously
// slipped through forever because it was "downloading" with progress > 0. With
// no activity past the timeout it must be failed just like a zero-progress one.
func TestPartialProgressStalledAccepted(t *testing.T) {
	p := newTestPlugin(t, nil) // default 4h
	e := sessionEntry("downloading", map[string]any{
		entry.FieldTorrentProgress:     50.0,
		entry.FieldTorrentLastActivity: now.Add(-5 * time.Hour),
		entry.FieldTorrentAddedAt:      now.Add(-72 * time.Hour),
	})
	classify(t, p, e)
	if !e.IsAccepted() {
		t.Fatalf("partially-downloaded stalled torrent should be accepted, reason=%q", e.RejectReason)
	}
}

// A fully-downloaded torrent is never failed, even if the client still labels it
// "downloading" and it has been inactive far past the timeout — removing it with
// data would delete a finished download.
func TestCompleteNeverFailed(t *testing.T) {
	p := newTestPlugin(t, nil)
	for _, state := range []string{"downloading", "seeding"} {
		e := sessionEntry(state, map[string]any{
			entry.FieldTorrentProgress:     100.0,
			entry.FieldTorrentLastActivity: now.Add(-500 * time.Hour),
			entry.FieldTorrentAddedAt:      now.Add(-1000 * time.Hour),
		})
		classify(t, p, e)
		if !e.IsRejected() {
			t.Errorf("complete torrent (state %s) should never be failed", state)
		}
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

// TestCompletedErroredTorrentNotFailed is the regression guard for the
// re-download loop: a torrent that already reached 100% and then errors has
// delivered its data, so failing it would let the janitor delete a finished
// download (torrent_control remove_with_data) and un-track a film that is
// already in the library — which re-downloads it on the next run.
func TestCompletedErroredTorrentNotFailed(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("errored", map[string]any{
		entry.FieldTorrentError:    "files missing from the download directory",
		entry.FieldTorrentProgress: 100.0,
	})
	classify(t, p, e)
	if e.IsAccepted() {
		t.Fatalf("a complete torrent must never be failed, accept reason = %q", e.AcceptReason)
	}
	if !strings.Contains(e.RejectReason, "complete") {
		t.Errorf("reject reason = %q, want it to name completeness", e.RejectReason)
	}
}

// An errored torrent that never finished is still a failed grab — the fix
// above must not make the janitor useless.
func TestIncompleteErroredTorrentStillFailed(t *testing.T) {
	p := newTestPlugin(t, nil)
	e := sessionEntry("errored", map[string]any{
		entry.FieldTorrentError:    "no space left on device",
		entry.FieldTorrentProgress: 41.5,
	})
	classify(t, p, e)
	if !e.IsAccepted() {
		t.Fatalf("an incomplete errored torrent should be failed, reason = %q", e.RejectReason)
	}
}

// A complete torrent that stalled past the timeout is likewise healthy: it has
// nothing left to download.
func TestCompletedStalledTorrentNotFailed(t *testing.T) {
	p := newTestPlugin(t, map[string]any{"stall_timeout": "1h"})
	e := sessionEntry("stalled", map[string]any{
		entry.FieldTorrentProgress:     100.0,
		entry.FieldTorrentLastActivity: now.Add(-72 * time.Hour),
	})
	classify(t, p, e)
	if e.IsAccepted() {
		t.Fatalf("a complete stalled torrent must not be failed, accept reason = %q", e.AcceptReason)
	}
}

// ── max_eta: the trickle the inactivity window cannot see ────────────────────

const gib = int64(1) << 30

// slowRig runs the plugin hour by hour against one torrent, the way the
// hourly janitor sees it.
type slowRig struct {
	t     *testing.T
	p     *failedPlugin
	clock time.Time
	added time.Time
}

func newSlowRig(t *testing.T, cfg map[string]any) *slowRig {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	r := &slowRig{t: t, p: p.(*failedPlugin), clock: now, added: now}
	r.p.now = func() time.Time { return r.clock }
	return r
}

// see classifies the torrent as it stands at the rig's clock: downloaded
// bytes so far of an 8 GiB torrent, at the given current rate.
func (r *slowRig) see(state string, downloaded, rate int64) *entry.Entry {
	r.t.Helper()
	e := sessionEntry(state, map[string]any{
		entry.FieldTorrentInfoHash:     "aaaa",
		entry.FieldTorrentAddedAt:      r.added,
		entry.FieldTorrentLastActivity: r.clock, // a trickle keeps this fresh
		entry.FieldTorrentFileSize:     8 * gib,
		entry.FieldTorrentProgress:     float64(downloaded) / float64(8*gib) * 100,
		entry.FieldTorrentDownloaded:   downloaded,
		entry.FieldTorrentDownRate:     rate,
	})
	classify(r.t, r.p, e)
	return e
}

var slowCfg = map[string]any{"max_eta": "96h"}

// The Batman Begins case: a few KB/s, steadily, for a whole day. Last
// activity is always fresh, so stall_timeout never fires; max_eta does.
func TestTrickleIsFailedOnceWatchedForAWindow(t *testing.T) {
	r := newSlowRig(t, slowCfg)
	const rate = 5 << 10 // 5 KiB/s: 8 GiB would take ~19 days
	var e *entry.Entry
	for h := 0; h <= 24; h++ {
		r.clock = now.Add(time.Duration(h) * time.Hour)
		e = r.see("downloading", int64(h)*3600*rate, rate)
		if h < 24 && e.IsAccepted() {
			t.Fatalf("failed after %dh, before a whole slow_window was watched: %s", h, e.AcceptReason)
		}
	}
	if !e.IsAccepted() || !strings.Contains(e.AcceptReason, "too slow") {
		t.Fatalf("a steady 5 KiB/s trickle should fail after 24h, state=%v reason=%q%q", e.State, e.RejectReason, e.AcceptReason)
	}
}

// One slow reading must not fail a torrent that has been fast.
func TestAMomentaryDipIsNotFailed(t *testing.T) {
	r := newSlowRig(t, slowCfg)
	const fast = 1 << 20 // 1 MiB/s
	for h := 0; h <= 24; h++ {
		r.clock = now.Add(time.Duration(h) * time.Hour)
		rate := int64(fast)
		if h == 24 {
			rate = 100 // the check happens to land on a lull
		}
		// 25.6 KiB/s on average: 2.2 GiB in the day, the rest in under
		// three days.
		e := r.see("downloading", int64(h)*3600*fast/40, rate)
		if e.IsAccepted() {
			t.Fatalf("hour %d: a torrent averaging 25.6 KiB/s was failed: %s", h, e.AcceptReason)
		}
		if h == 24 && !strings.Contains(e.RejectReason, "below stall_timeout") {
			t.Fatalf("hour 24: want the torrent judged and kept, got %q", e.RejectReason)
		}
	}
}

// A torrent that crawled all day and has just sped up is kept, because the
// current rate counts.
func TestASpeedUpRescuesASlowTorrent(t *testing.T) {
	r := newSlowRig(t, slowCfg)
	const crawl = 1 << 10
	var got int64
	for h := 0; h <= 24; h++ {
		r.clock = now.Add(time.Duration(h) * time.Hour)
		rate := int64(crawl)
		if h == 24 {
			rate = 5 << 20 // 5 MiB/s now: done in well under max_eta
		}
		got += 3600 * crawl
		e := r.see("downloading", got, rate)
		if h == 24 && e.IsAccepted() {
			t.Fatalf("a torrent now at 5 MiB/s was failed: %s", e.AcceptReason)
		}
	}
}

// The watch must be continuous. A torrent paused or queued in between was
// not downloading, so the gap is not a slow spell: its watch starts over.
func TestAPauseRestartsTheWatch(t *testing.T) {
	r := newSlowRig(t, slowCfg)
	const rate = 1 << 10
	for h := 0; h < 20; h++ {
		r.clock = now.Add(time.Duration(h) * time.Hour)
		r.see("downloading", int64(h)*3600*rate, rate)
	}
	r.clock = now.Add(20 * time.Hour)
	r.see("paused", 20*3600*rate, 0)
	for h := 21; h <= 30; h++ {
		r.clock = now.Add(time.Duration(h) * time.Hour)
		if e := r.see("downloading", int64(h)*3600*rate, rate); e.IsAccepted() {
			t.Fatalf("hour %d: failed only %dh after the watch restarted: %s", h, h-21, e.AcceptReason)
		}
	}
}

// A re-added torrent starts its counter again; that is a new download, not
// a negative rate.
func TestAReAddRestartsTheWatch(t *testing.T) {
	r := newSlowRig(t, slowCfg)
	const rate = 1 << 10
	for h := 0; h <= 23; h++ {
		r.clock = now.Add(time.Duration(h) * time.Hour)
		r.see("downloading", 4*gib+int64(h)*3600*rate, rate)
	}
	r.clock = now.Add(25 * time.Hour)
	r.added = r.clock
	if e := r.see("downloading", 0, rate); e.IsAccepted() {
		t.Fatalf("a re-added torrent was failed on its first sighting: %s", e.AcceptReason)
	}
}

// A torrent that leaves the session takes its samples with it.
func TestSamplesOfGoneTorrentsAreDropped(t *testing.T) {
	r := newSlowRig(t, slowCfg)
	r.see("downloading", 1<<20, 1<<10)
	if keys, _ := r.p.rates.Keys(); len(keys) != 1 {
		t.Fatalf("keys = %v, want the watched torrent", keys)
	}
	if _, err := r.p.Process(context.Background(), makeCtx(), nil); err != nil {
		t.Fatal(err)
	}
	if keys, _ := r.p.rates.Keys(); len(keys) != 0 {
		t.Errorf("keys = %v, want none once the torrent is gone", keys)
	}
}

// Without max_eta nothing changes: a trickle is never failed, and nothing is
// stored.
func TestNoMaxETAKeepsTheTrickle(t *testing.T) {
	r := newSlowRig(t, nil)
	for h := 0; h <= 48; h++ {
		r.clock = now.Add(time.Duration(h) * time.Hour)
		if e := r.see("downloading", int64(h)*3600*100, 100); e.IsAccepted() {
			t.Fatalf("hour %d: failed without max_eta: %s", h, e.AcceptReason)
		}
	}
	if keys, _ := r.p.rates.Keys(); len(keys) != 0 {
		t.Errorf("keys = %v, want nothing stored without max_eta", keys)
	}
}

func TestSlowWindowNeedsMaxETA(t *testing.T) {
	if errs := validate(map[string]any{"slow_window": "12h"}); len(errs) == 0 {
		t.Error("slow_window without max_eta should be refused")
	}
	if errs := validate(map[string]any{"max_eta": "96h", "slow_window": "12h"}); len(errs) != 0 {
		t.Errorf("valid config refused: %v", errs)
	}
	if errs := validate(map[string]any{"max_eta": "soon"}); len(errs) == 0 {
		t.Error("a malformed max_eta should be refused")
	}
}
