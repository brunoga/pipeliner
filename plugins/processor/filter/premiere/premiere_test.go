package premiere

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/grabs"
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
			Year:        ep.SeriesYear,
			Proper:      ep.Proper,
			Repack:      ep.Repack,
		},
		Season:        ep.Season,
		Episode:       ep.Episode,
		EpisodeID:     series.EpisodeID(ep),
		DoubleEpisode: ep.DoubleEpisode,
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
		entry.FieldQuality:         false, // quality spec match + persisted record read e.Quality()
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

// TestProcessStampsMediaTypeSeries verifies that every entry the filter
// processes — accepted or rejected — has media_type=series stamped on it so
// downstream classifiers can rely on it.
func TestProcessStampsMediaTypeSeries(t *testing.T) {
	p := makePlugin(t, nil)
	a := makeEntry("Brand New Show", 1, 1)
	b := makeEntry("Another Show", 1, 1)

	out, err := p.Process(context.Background(), makeCtx(), []*entry.Entry{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 entries out, got %d", len(out))
	}
	for _, e := range out {
		if got := e.GetString(entry.FieldMediaType); got != entry.MediaTypeSeries {
			t.Errorf("entry %q: media_type = %q, want %q", e.Title, got, entry.MediaTypeSeries)
		}
	}
}

// Brothers (2026): the premiere was downloaded as "Brothers 2026 S01E01" and
// again a week later as "Brothers S01E01 2026", because the two spellings
// keyed two different shows.
func TestPremiereSeenUnderEitherYearSpelling(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	tc := makeCtx()

	first := rawEntry("Brothers 2026 S01E01 On the Road 2160p ATVP WEB-DL DDP5 1 Atmos DV HDR H 265-RAWR", "http://x/1")
	filter(t, p, first)
	if !first.IsAccepted() {
		t.Fatalf("first spelling should be accepted: %s", first.RejectReason)
	}
	if err := p.Commit(context.Background(), tc, []*entry.Entry{first}); err != nil {
		t.Fatal(err)
	}

	for _, title := range []string{
		"Brothers S01E01 2026 1080p ATVP WEB-DL H 264 DDP5 1 Atmos-HHWEB",
		"Brothers S01E01 1080p WEB h264-GRP",
	} {
		e := rawEntry(title, "http://x/2")
		filter(t, p, e)
		if !e.IsRejected() {
			t.Errorf("%q: premiere already downloaded, should be rejected", title)
		}
	}
}

// A premiere tracked before shows were keyed by name and year, under the bare
// name, still counts once the year is known.
func TestPremiereSeenUnderLegacyKey(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	if err := p.tracker.Mark(series.Record{SeriesName: "last seen", EpisodeID: "S01E01"}); err != nil {
		t.Fatal(err)
	}
	e := rawEntry("Last Seen 2026 S01E01 The Truth 1080p ATVP WEB-DL DDP5 1 Atmos H 264-RAWR", "http://x/1")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("premiere tracked under the bare name should be rejected")
	}
}

// A premiere whose torrent dies must be retried. The torrent sinks copy the
// tracker key into the grab record, and mark_failed forgets the episode under
// that key; a premiere that did not stamp it was never un-tracked, so its show
// was treated as downloaded for good.
func TestFailedPremiereGrabIsUntracked(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	tc := makeCtx()

	e := rawEntry("Brothers 2026 S01E01 On the Road 2160p ATVP WEB-DL DDP5 1 Atmos DV HDR H 265-RAWR", "http://x/1")
	filter(t, p, e)
	if !e.IsAccepted() {
		t.Fatalf("premiere should be accepted: %s", e.RejectReason)
	}
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// What the deluge/transmission/qbittorrent sinks store at add time.
	rec := grabs.FromEntry(e, tc.Name)
	if rec.SeriesName != "brothers 2026" || rec.EpisodeID != "S01E01" {
		t.Fatalf("grab record: got series %q episode %q, want %q %q",
			rec.SeriesName, rec.EpisodeID, "brothers 2026", "S01E01")
	}

	// What mark_failed does when the janitor purges the torrent.
	if err := p.tracker.Forget(rec.SeriesName, rec.EpisodeID); err != nil {
		t.Fatal(err)
	}

	retry := rawEntry("Brothers 2026 S01E01 On the Road 1080p ATVP WEB-DL DDP5 1 Atmos H 264-FLUX", "http://x/2")
	filter(t, p, retry)
	if !retry.IsAccepted() {
		t.Errorf("premiere should be retried after the failed grab: %s", retry.RejectReason)
	}
}

// TestCommitUsesDecisionEpisodeID is the regression guard for wrong-key
// writes. metainfo_tvdb re-parses the release name and overwrites
// series_episode_id, and it sits DOWNSTREAM of this filter — while Commit runs
// last of all. Reading the field at commit time stored the record against a
// different episode than the decision was about.
func TestCommitUsesDecisionEpisodeID(t *testing.T) {
	p := makePlugin(t, map[string]any{"season": 1, "episode": 1})
	tc := makeCtx()
	e := makeEntry("New Show", 1, 1)

	if _, err := p.Process(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Fatalf("a season-1 premiere should be accepted, reason = %q", e.RejectReason)
	}
	decided := e.GetString(entry.FieldSeriesTrackerEpisodeID)
	if decided != "S01E01" {
		t.Fatalf("stamped episode id = %q, want S01E01", decided)
	}

	// Downstream enrichment re-parses the release onto a different episode.
	e.Set(entry.FieldSeriesEpisodeID, "S01E03")
	e.Set(entry.FieldSeriesEpisode, 3)

	if err := p.Commit(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !p.tracker.IsSeen("new show", "S01E01") {
		t.Error("tracker must hold the S01E01 key the decision used")
	}
	if p.tracker.IsSeen("new show", "S01E03") {
		t.Error("the rewritten episode id must not be tracked")
	}
}
