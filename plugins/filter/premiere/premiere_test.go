package premiere

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func makePlugin(t *testing.T, cfg map[string]any) *premierePlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*premierePlugin)
}

func makeEntry(seriesName string, season, episode int) *entry.Entry {
	slug := strings.ReplaceAll(seriesName, " ", ".")
	title := fmt.Sprintf("%s.S%02dE%02d.720p.HDTV", slug, season, episode)
	return entry.New(title, "http://example.com/"+slug+".torrent")
}

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test-task",
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func filter(t *testing.T, p *premierePlugin, e *entry.Entry) {
	t.Helper()
	if err := p.Filter(context.Background(), makeCtx(), e); err != nil {
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
	tc := makeCtx()

	// First run — accept then persist via Learn.
	e1 := makeEntry("Breaking Bad", 1, 1)
	if err := p.Filter(context.Background(), tc, e1); err != nil {
		t.Fatal(err)
	}
	if !e1.IsAccepted() {
		t.Fatal("first run should accept")
	}
	if err := p.Learn(context.Background(), tc, []*entry.Entry{e1}); err != nil {
		t.Fatal(err)
	}

	// Second run — same series should now be rejected.
	e2 := makeEntry("Breaking Bad", 1, 1)
	if err := p.Filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsRejected() {
		t.Error("second run should reject already-seen premiere")
	}
}

func TestMultipleEntriesSameSeriesAllAccepted(t *testing.T) {
	p := makePlugin(t, map[string]any{})

	// Multiple entries for the same unseen premiere should all be accepted
	// so the dedup step can pick the best one.
	e1 := makeEntry("Breaking Bad", 1, 1)
	e2 := makeEntry("Breaking Bad", 1, 1)
	filter(t, p, e1)
	filter(t, p, e2)
	if !e1.IsAccepted() || !e2.IsAccepted() {
		t.Error("all entries for the same unseen premiere should be accepted")
	}
}

func TestNonEpisodeRejectedByDefault(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	e := entry.New("random.file.mkv", "http://example.com/file.mkv")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Errorf("entry that does not parse as an episode should be rejected by default, got: %s", e.State)
	}
}

func TestNonEpisodeUndecidedOptOut(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject_unmatched": false})
	e := entry.New("random.file.mkv", "http://example.com/file.mkv")
	filter(t, p, e)
	if !e.IsUndecided() {
		t.Errorf("entry that does not parse as an episode should be undecided when reject_unmatched is false, got: %s", e.State)
	}
}

func TestDifferentSeriesIndependent(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	tc := makeCtx()

	e1 := makeEntry("Breaking Bad", 1, 1)
	_ = p.Filter(context.Background(), tc, e1)

	// Different series — should also be accepted.
	e2 := makeEntry("The Wire", 1, 1)
	_ = p.Filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("premiere of different series should be accepted; reason: %q", e2.RejectReason)
	}
}
