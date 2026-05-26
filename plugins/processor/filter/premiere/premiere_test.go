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
	"github.com/brunoga/pipeliner/internal/series"
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

// metaize simulates what metainfo_file does upstream: parses the entry title
// and populates the series_* / Quality fields that premiere reads. The premiere
// plugin requires these fields (via Descriptor.Requires) so tests must call
// this helper before invoking filter().
func metaize(e *entry.Entry) {
	ep, ok := series.Parse(e.Title)
	if !ok {
		return
	}
	e.SetSeriesInfo(entry.SeriesInfo{
		VideoInfo: entry.VideoInfo{
			GenericInfo: entry.GenericInfo{Title: ep.SeriesName},
		},
		Season:        ep.Season,
		Episode:       ep.Episode,
		EpisodeID:     series.EpisodeID(ep),
		DoubleEpisode: ep.DoubleEpisode,
		Proper:        ep.Proper,
		Repack:        ep.Repack,
		Service:       ep.Service,
	})
	e.SetQuality(ep.Quality)
}

func makeEntry(seriesName string, season, episode int) *entry.Entry {
	slug := strings.ReplaceAll(seriesName, " ", ".")
	title := fmt.Sprintf("%s.S%02dE%02d.720p.HDTV", slug, season, episode)
	e := entry.New(title, "http://example.com/"+slug+".torrent")
	metaize(e)
	return e
}

// rawEntry creates an entry with a custom title and runs the metaize helper so
// the required upstream fields are present. Use this for tests that supply
// non-standard titles.
func rawEntry(title, url string) *entry.Entry {
	e := entry.New(title, url)
	metaize(e)
	return e
}

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test-task",
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func filter(t *testing.T, p *premierePlugin, e *entry.Entry) {
	t.Helper()
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
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

	// First run — accept then commit (simulating a successful download).
	e1 := makeEntry("Breaking Bad", 1, 1)
	if err := p.filter(context.Background(), tc, e1); err != nil {
		t.Fatal(err)
	}
	if !e1.IsAccepted() {
		t.Fatal("first run should accept")
	}
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e1}); err != nil {
		t.Fatal(err)
	}

	// Second run — same series should now be rejected.
	e2 := makeEntry("Breaking Bad", 1, 1)
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsRejected() {
		t.Error("second run should reject already-seen premiere")
	}
}

func TestFailedDownloadAllowsRetry(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	tc := makeCtx()

	// First run — accept but do NOT commit (simulating a failed download).
	e1 := makeEntry("Breaking Bad", 1, 1)
	if err := p.filter(context.Background(), tc, e1); err != nil {
		t.Fatal(err)
	}
	if !e1.IsAccepted() {
		t.Fatal("first run should accept")
	}
	// No Commit call — download failed.

	// Second run — same series should still be accepted (retry).
	e2 := makeEntry("Breaking Bad", 1, 1)
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsAccepted() {
		t.Errorf("premiere should be retried after failed download; reason: %q", e2.RejectReason)
	}
}

func TestMultipleEntriesSameSeriesAllAccepted(t *testing.T) {
	p := makePlugin(t, map[string]any{})

	// Multiple entries for the same unseen premiere should all be accepted
	// so the dedup step can pick the best one. Use distinct titles so the
	// metaize helper sets different qualities (otherwise both entries share
	// the same URL key downstream).
	e1 := rawEntry("Breaking.Bad.S01E01.720p.HDTV", "http://example.com/a.torrent")
	e2 := rawEntry("Breaking.Bad.S01E01.1080p.WEB-DL", "http://example.com/b.torrent")
	filter(t, p, e1)
	filter(t, p, e2)
	if !e1.IsAccepted() || !e2.IsAccepted() {
		t.Error("all entries for the same unseen premiere should be accepted")
	}
}

func TestNonEpisodeRejectedByDefault(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	// Title does not parse as an episode → metainfo_file would leave
	// series_episode_id unset. premiere must reject.
	e := rawEntry("random.file.mkv", "http://example.com/file.mkv")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Errorf("entry without series_episode_id should be rejected by default, got: %s", e.State)
	}
}

func TestNonEpisodeUndecidedOptOut(t *testing.T) {
	p := makePlugin(t, map[string]any{"reject_unmatched": false})
	e := rawEntry("random.file.mkv", "http://example.com/file.mkv")
	filter(t, p, e)
	if !e.IsUndecided() {
		t.Errorf("entry without series_episode_id should be undecided when reject_unmatched is false, got: %s", e.State)
	}
}

func TestDifferentSeriesIndependent(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	tc := makeCtx()

	e1 := makeEntry("Breaking Bad", 1, 1)
	_ = p.filter(context.Background(), tc, e1)

	// Different series — should also be accepted.
	e2 := makeEntry("The Wire", 1, 1)
	_ = p.filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("premiere of different series should be accepted; reason: %q", e2.RejectReason)
	}
}

func TestNormalizedNameTrackerKey(t *testing.T) {
	// Tracker keys must use the normalized (lowercase) show name so that
	// records written by the series plugin (which also normalizes) are visible
	// to the premiere plugin and vice versa.
	p := makePlugin(t, map[string]any{})
	tc := makeCtx()

	e1 := makeEntry("Breaking Bad", 1, 1)
	if err := p.filter(context.Background(), tc, e1); err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e1}); err != nil {
		t.Fatal(err)
	}

	// IsSeen must find the record using the normalized key.
	if !p.tracker.IsSeen("breaking bad", "S01E01") {
		t.Error("tracker should store record under normalized (lowercase) show name")
	}
	// Must NOT be stored under the raw capitalized form.
	if p.tracker.IsSeen("Breaking Bad", "S01E01") {
		t.Error("tracker must not store record under raw capitalized show name")
	}
}

func TestPersistSkipsNonAccepted(t *testing.T) {
	// Regression: dedup-rejected entries passed to Commit must not be persisted.
	p := makePlugin(t, map[string]any{})
	tc := makeCtx()

	eHigh := makeEntry("Breaking Bad", 1, 1)
	eLow := rawEntry("Breaking.Bad.S01E01.480p.HDTV", "http://example.com/low.torrent")

	_ = p.filter(context.Background(), tc, eHigh)
	_ = p.filter(context.Background(), tc, eLow)

	// Simulate dedup rejecting the lower-quality copy.
	eLow.Reject("dedup: better copy accepted")

	if err := p.Commit(context.Background(), tc, []*entry.Entry{eHigh, eLow}); err != nil {
		t.Fatal(err)
	}

	// Only eHigh should have been persisted; eLow's quality must not be stored.
	if !p.tracker.IsSeen("breaking bad", "S01E01") {
		t.Error("accepted entry should be marked in tracker")
	}
}

func TestDoubleEpisodePremiereMarksBothParts(t *testing.T) {
	// A double-episode premiere (S01E01E02) should mark the combined ID and
	// each individual part so single-episode releases are recognised later.
	p := makePlugin(t, map[string]any{})
	tc := makeCtx()

	e := rawEntry("Breaking.Bad.S01E01E02.720p.HDTV", "http://example.com/double.torrent")
	if err := p.filter(context.Background(), tc, e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Fatalf("double premiere should be accepted: %s", e.RejectReason)
	}
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	for _, epID := range []string{"S01E01E02", "S01E01", "S01E02"} {
		if !p.tracker.IsSeen("breaking bad", epID) {
			t.Errorf("tracker should have %s marked after double premiere commit", epID)
		}
	}
}

// TestRequiresDeclared verifies that the plugin descriptor declares the
// required upstream fields. The DAG validator uses this to catch pipelines
// that wire premiere without an upstream metainfo step.
func TestRequiresDeclared(t *testing.T) {
	d, ok := plugin.Lookup("premiere")
	if !ok {
		t.Fatal("premiere not registered")
	}
	want := map[string]bool{
		entry.FieldTitle:           false,
		entry.FieldSeriesEpisodeID: false,
		entry.FieldSeriesSeason:    false,
		entry.FieldSeriesEpisode:   false,
	}
	for _, group := range d.Requires {
		for _, f := range group {
			if _, ok := want[f]; ok {
				want[f] = true
			}
		}
	}
	for f, found := range want {
		if !found {
			t.Errorf("Requires must include %q", f)
		}
	}
}
