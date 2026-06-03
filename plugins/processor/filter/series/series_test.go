package series

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

// mockInput is a SourcePlugin returning a fixed list of entries.
type mockInput struct {
	entries []*entry.Entry
}

func (m *mockInput) Name() string { return "mock_input" }
func (m *mockInput) Generate(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	return m.entries, nil
}

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func openPlugin(t *testing.T, extra map[string]any) *seriesPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := map[string]any{
		"static": []any{"My Show"},
	}
	maps.Copy(cfg, extra)
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*seriesPlugin)
}

// metaize simulates what metainfo_file does upstream: parses the entry title
// and populates the series_* / Quality fields that series reads. The series
// plugin requires these fields (via Descriptor.Requires) so tests must call
// this helper before invoking filter() or Process().
func metaize(e *entry.Entry) {
	ep, ok := series.Parse(e.Title)
	if !ok {
		return
	}
	e.SetSeriesInfo(entry.SeriesInfo{
		VideoInfo: entry.VideoInfo{
			GenericInfo: entry.GenericInfo{Title: ep.SeriesName},
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

// makeEntry builds an entry with the given title and runs metaize so the
// fields series reads are present (simulating metainfo_file upstream).
func makeEntry(title, url string) *entry.Entry {
	e := entry.New(title, url)
	metaize(e)
	return e
}

// --- Basic accept/reject ---

func TestAcceptsKnownShow(t *testing.T) {
	p := openPlugin(t, nil)
	e := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("known show episode should be accepted: %s", e.RejectReason)
	}
}

func TestRejectsUnknownShowByDefault(t *testing.T) {
	p := openPlugin(t, nil)
	e := makeEntry("Other.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsRejected() {
		t.Error("unknown show should be rejected by default")
	}
}

func TestRejectsNonEpisodeByDefault(t *testing.T) {
	p := openPlugin(t, nil)
	// metaize sets no series_* fields when the title doesn't parse as an
	// episode — series_episode_id is empty so series rejects with the
	// "no series_episode_id" path.
	e := makeEntry("Just A Movie 2023 1080p BluRay", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsRejected() {
		t.Error("entry without series_episode_id should be rejected by default")
	}
}

func TestUnmatchedOptOut(t *testing.T) {
	p := openPlugin(t, map[string]any{"reject_unmatched": false})
	e := makeEntry("Other.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if e.IsAccepted() || e.IsRejected() {
		t.Error("unknown show should be left undecided when reject_unmatched is false")
	}
}

// --- Seen deduplication ---

func TestDuplicateRejected(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()
	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("already-downloaded episode should be rejected")
	}
}

// --- Quality upgrade ---

func TestQualityUpgradesExisting(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Download a 720p copy.
	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	// A plain 1080p copy (no PROPER/REPACK) should be accepted as an upgrade.
	e2 := makeEntry("My.Show.S01E01.1080p.BluRay", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("higher-quality copy should be accepted: %s", e2.RejectReason)
	}
}

func TestProperUpgradesExisting(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Download a 720p copy.
	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	// A PROPER 1080p copy should also be accepted as an upgrade.
	e2 := makeEntry("My.Show.S01E01.PROPER.1080p.BluRay", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("proper higher-quality copy should be accepted: %s", e2.RejectReason)
	}
}

func TestProperSameQualityUpgradesExisting(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	// PROPER at identical quality specs should still be accepted (fixes content issues).
	e2 := makeEntry("My.Show.S01E01.PROPER.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("proper at same quality should be accepted: %s", e2.RejectReason)
	}
}

// Regression for the REPACK loop bug: once a REPACK is recorded, a subsequent
// REPACK at the same quality must NOT be re-accepted, otherwise the same
// release would download on every run as long as it stays in the feed.
func TestRepackOverStoredRepackRejected(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.REPACK.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := makeEntry("My.Show.S01E01.REPACK.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Errorf("second REPACK at same quality must be rejected (loop guard); got accepted: %s", e2.AcceptReason)
	}
}

func TestNoUpgradeWhenQualityNotBetter(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.720p.BluRay", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	// Lower source quality (HDTV < BluRay) — should be rejected even with PROPER tag.
	e2 := makeEntry("My.Show.S01E01.PROPER.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("copy with lower quality should not replace existing")
	}
}

// TestUpgradeNonLatestEpisode is a regression test for the bug where quality
// upgrades were only accepted for the most recently downloaded episode.
// Previously the upgrade check used Latest() and gated on latest.EpisodeID ==
// epID, so a better-quality release for an older episode was silently rejected
// as "already downloaded" once a newer episode had been recorded.
func TestUpgradeNonLatestEpisode(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Download S01E01 at 720p, then S01E02 at 720p (making S01E02 the "latest").
	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	e2 := makeEntry("My.Show.S01E02.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})
	p.filter(context.Background(), tc, e2)
	p.persist(context.Background(), tc, []*entry.Entry{e2})

	// A 1080p BluRay of S01E01 should be accepted as an upgrade even though
	// S01E02 is the most recently downloaded episode.
	eUpgrade := makeEntry("My.Show.S01E01.1080p.BluRay", "http://x.com/c")
	p.filter(context.Background(), tc, eUpgrade)
	if !eUpgrade.IsAccepted() {
		t.Errorf("higher-quality copy of older episode should be accepted as upgrade: %s", eUpgrade.RejectReason)
	}
}

// TestPersistOnlyStoresAcceptedEntries verifies that dedup-rejected entries
// passed to Commit do not overwrite the quality stored for the accepted copy.
// Regression: the executor passes all entries produced by the series node to
// Commit (not just the accepted one), so persist must skip non-accepted entries.
func TestPersistOnlyStoresAcceptedEntries(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Two copies of S01E01: BluRay (accepted by dedup) and HDTV (rejected by dedup).
	// Both pass the series filter initially.
	eHigh := makeEntry("My.Show.S01E01.1080p.BluRay.x264", "http://x.com/high")
	eLow := makeEntry("My.Show.S01E01.480p.HDTV", "http://x.com/low")

	p.filter(context.Background(), tc, eHigh)
	p.filter(context.Background(), tc, eLow)

	// Simulate what dedup does: reject the lower-quality copy.
	eLow.Reject("dedup: better copy already accepted")

	// Executor passes both to Commit; persist must only store the accepted one.
	if err := p.persist(context.Background(), tc, []*entry.Entry{eHigh, eLow}); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// If eLow's HDTV quality was stored (the bug), an HDTV copy in the next run
	// would appear as an "upgrade" over stored HDTV — it should instead be rejected
	// as a same-quality copy.  If eHigh's BluRay quality was stored (the fix), the
	// HDTV copy is correctly rejected as a downgrade.
	eDowngrade := makeEntry("My.Show.S01E01.1080p.HDTV", "http://x.com/downgrade")
	p.filter(context.Background(), tc, eDowngrade)
	if !eDowngrade.IsRejected() {
		t.Error("HDTV copy should be rejected as a downgrade when BluRay is stored; " +
			"if this fails, a rejected entry's quality was stored instead of the accepted one")
	}
}

// TestDoViParsesAsDolbyVision verifies that the "DoVi" abbreviation (used by
// playWEB and similar groups) is recognised as Dolby Vision, not unknown.
// Regression: previously "DoVi" was not matched by the reColorRange regex,
// causing DoVi releases to appear as ColorRange=Unknown which could then pass
// a quality-upgrade check against a stored Dolby Vision entry.
func TestDoViParsesAsDolbyVision(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Store a 2160p Dolby Vision (DV) copy.
	eDV := makeEntry("My.Show.S01E01.2160p.AMZN.WEB-DL.Atmos.DV.H.265-FLUX", "http://x.com/dv")
	p.filter(context.Background(), tc, eDV)
	p.persist(context.Background(), tc, []*entry.Entry{eDV})

	// A DoVi-labeled copy at the same resolution/source should be rejected
	// (same quality, not an upgrade).
	eDoVi := makeEntry("My.Show.S01E01.2160p.AMZN.WEB-DL.Atmos.DoVi.H.265-playWEB", "http://x.com/dovi")
	p.filter(context.Background(), tc, eDoVi)
	if !eDoVi.IsRejected() {
		t.Errorf("DoVi copy at same quality should be rejected; reason: %s", eDoVi.RejectReason)
	}

	// An HDR (non-DV) copy at the same resolution/source should also be rejected
	// since HDR < Dolby Vision.
	eHDR := makeEntry("My.Show.S01E01.2160p.AMZN.WEB-DL.Atmos.HDR.H.265-playWEB", "http://x.com/hdr")
	p.filter(context.Background(), tc, eHDR)
	if !eHDR.IsRejected() {
		t.Errorf("HDR copy should be rejected when DV already stored; reason: %s", eHDR.RejectReason)
	}
}

// --- Strict tracking ---

func TestStrictAllowsNext(t *testing.T) {
	p := openPlugin(t, map[string]any{"tracking": "strict"})
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := makeEntry("My.Show.S01E02.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("next episode should be accepted in strict mode: %s", e2.RejectReason)
	}
}

func TestStrictRejectsSkip(t *testing.T) {
	p := openPlugin(t, map[string]any{"tracking": "strict"})
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e3 := makeEntry("My.Show.S01E04.720p.HDTV", "http://x.com/c")
	p.filter(context.Background(), tc, e3)
	if !e3.IsRejected() {
		t.Error("strict mode should reject an episode that skips ahead by 3")
	}
}

func TestBackfillAllowsOlder(t *testing.T) {
	p := openPlugin(t, map[string]any{"tracking": "backfill"})
	tc := makeCtx()

	e5 := makeEntry("My.Show.S01E05.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e5)
	p.persist(context.Background(), tc, []*entry.Entry{e5})

	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e1)
	if !e1.IsAccepted() {
		t.Errorf("backfill mode should accept older episodes: %s", e1.RejectReason)
	}
}

// --- Quality filter ---

func TestQualityGateRejects(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "720p"})
	e := makeEntry("My.Show.S01E01.480p.HDTV", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsRejected() {
		t.Error("quality gate should reject 480p when spec is 720p")
	}
}

func TestQualityGateAccepts(t *testing.T) {
	// "720p+" means 720p or better — 1080p should pass.
	p := openPlugin(t, map[string]any{"quality": "720p+"})
	e := makeEntry("My.Show.S01E01.1080p.BluRay", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("1080p should pass 720p+ quality gate: %s", e.RejectReason)
	}
}

func TestQualityGateExact(t *testing.T) {
	// "720p" (no +) means exactly 720p — 1080p should be rejected.
	p := openPlugin(t, map[string]any{"quality": "720p"})
	e := makeEntry("My.Show.S01E01.1080p.BluRay", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsRejected() {
		t.Error("1080p should be rejected by exact 720p quality spec")
	}
}

func TestQualityGateBlocksUpgradeOutsideSpec(t *testing.T) {
	// Spec "720p-1080p" means upgrades must stay inside the range. Even when
	// a stored 1080p exists and an incoming 2160p is technically "better",
	// the spec is an absolute gate and 2160p must be rejected.
	p := openPlugin(t, map[string]any{"quality": "720p-1080p"})
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.1080p.BluRay", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})
	if !e1.IsAccepted() {
		t.Fatalf("1080p inside spec should be accepted: %s", e1.RejectReason)
	}

	e2 := makeEntry("My.Show.S01E01.2160p.BluRay", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Errorf("2160p outside spec 720p-1080p must be rejected even as upgrade; got accepted (%s)", e2.AcceptReason)
	}
}

// --- Fuzzy matching ---

func TestFuzzyMatchExact(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"my show", "my show", true},
		{"my show", "my sho", false},  // 1 edit (drop w) — must not match
		{"my show", "mi show", false}, // 1 edit (y→i) — must not match
		{"completely different", "my show", false},
		{"my show", "my show 2", false},
		{"my show", "my show extra", false},
	}
	for _, tc := range cases {
		got := match.Fuzzy(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("match.Fuzzy(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// --- Config validation ---

// TestEmptyConfigAcceptsAll documents the no-list path: a series filter with
// no static and no list is valid and behaves as an accept-all filter (gated
// only by the upstream Requires, the quality spec, and the tracker).
func TestEmptyConfigAcceptsAll(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	p, err := newPlugin(map[string]any{}, db)
	if err != nil {
		t.Fatalf("empty config should be valid for accept-all mode: %v", err)
	}
	sp := p.(*seriesPlugin)
	if sp.hasList() {
		t.Error("hasList should be false with no static or list")
	}
	e := makeEntry("Show.Never.Listed.S01E01.720p.HDTV", "http://x.com/a")
	if err := sp.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("accept-all filter should accept any classified episode; got %v: %s", e.State, e.RejectReason)
	}
}

func TestUnknownTrackingMode(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{
		"shows":    []any{"My Show"},
		"tracking": "invalid",
	}, db)
	if err == nil {
		t.Error("expected error for unknown tracking mode")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("series")
	if !ok {
		t.Fatal("series plugin not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("phase: got %v", d.Role)
	}
}

// TestRequiresDeclared verifies that the plugin descriptor declares the
// required upstream fields. The DAG validator uses this to catch pipelines
// that wire series without an upstream metainfo step.
func TestRequiresDeclared(t *testing.T) {
	d, ok := plugin.Lookup("series")
	if !ok {
		t.Fatal("series not registered")
	}
	want := map[string]bool{
		entry.FieldTitle:           false,
		entry.FieldSeriesEpisodeID: false,
		entry.FieldSeriesSeason:    false, // follow-mode season floor + double-episode persist
		entry.FieldSeriesEpisode:   false, // double-episode persist
		entry.FieldQuality:         false, // quality features (spec, upgrade) read e.Quality()
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

// --- Dynamic list ---

func openWithFrom(t *testing.T, mock *mockInput) *seriesPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return &seriesPlugin{
		listSources: []plugin.SourcePlugin{mock},
		listCache:   cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("test")),
		tracking:    trackingBackfill,
		tracker:     series.NewTracker(db.Bucket("series")),
	}
}

func TestFromAcceptsDynamicShow(t *testing.T) {
	mock := &mockInput{entries: []*entry.Entry{
		entry.New("My Dynamic Show", ""),
	}}
	p := openWithFrom(t, mock)

	e := makeEntry("My.Dynamic.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("dynamic show should be accepted: %s", e.RejectReason)
	}
}

func TestFromIgnoresUnlistedShow(t *testing.T) {
	mock := &mockInput{entries: []*entry.Entry{
		entry.New("My Dynamic Show", ""),
	}}
	p := openWithFrom(t, mock)

	e := makeEntry("Other.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("unlisted show should be undecided; got %v", e.State)
	}
}

func TestFromCachesResults(t *testing.T) {
	callCount := 0
	mock := &mockInput{entries: []*entry.Entry{entry.New("Breaking Bad", "")}}
	// Use a wrapper so we can count calls.
	counted := &countingInput{wrapped: mock, count: &callCount}
	db, _ := store.OpenSQLite(":memory:")
	p := &seriesPlugin{
		listSources: []plugin.SourcePlugin{counted},
		listCache:   cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("test")),
		tracking:    trackingBackfill,
		tracker:     series.NewTracker(db.Bucket("series")),
	}
	tc := makeCtx()
	p.resolveShows(context.Background(), tc)
	p.resolveShows(context.Background(), tc)
	if callCount != 1 {
		t.Errorf("from input should be called once due to caching; got %d calls", callCount)
	}
}

func TestFromEmptyResultNotCached(t *testing.T) {
	callCount := 0
	mock := &mockInput{} // returns no entries
	counted := &countingInput{wrapped: mock, count: &callCount}
	db, _ := store.OpenSQLite(":memory:")
	p := &seriesPlugin{
		listSources: []plugin.SourcePlugin{counted},
		listCache:   cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("test")),
		tracking:    trackingBackfill,
		tracker:     series.NewTracker(db.Bucket("series")),
	}
	tc := makeCtx()
	p.resolveShows(context.Background(), tc)
	p.resolveShows(context.Background(), tc)
	if callCount != 2 {
		t.Errorf("empty from result should not be cached; plugin called %d times, want 2", callCount)
	}
}

type countingInput struct {
	wrapped *mockInput
	count   *int
}

func (c *countingInput) Name() string { return "counting_input" }
func (c *countingInput) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	*c.count++
	return c.wrapped.Generate(ctx, tc)
}

func TestMultipleEntriesSameEpisodeAllAccepted(t *testing.T) {
	// Multiple entries for the same episode (different quality/source) should all
	// be accepted by series.Process so the dedup processor can pick the best one.
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	e2 := makeEntry("My.Show.S01E01.1080p.WEB-DL", "http://x.com/b")
	e3 := makeEntry("My.Show.S01E01.2160p.BluRay", "http://x.com/c")

	for _, e := range []*entry.Entry{e1, e2, e3} {
		p.filter(context.Background(), tc, e)
	}

	for _, e := range []*entry.Entry{e1, e2, e3} {
		if !e.IsAccepted() {
			t.Errorf("entry %q should be accepted before dedup: %s", e.Title, e.RejectReason)
		}
	}
}

func TestMultipleEntriesSameEpisodeLearnRejectsOldOnNextRun(t *testing.T) {
	// After Learn persists one accepted entry, subsequent runs reject that episode.
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.1080p.WEB-DL", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("episode already in tracker should be rejected on next run")
	}
}

// TestQualitySpecAppliesWithoutList exercises the no-list code path with a
// quality spec: the spec is still an absolute gate, so an episode below it
// gets rejected even when there's no title list to match against.
func TestQualitySpecAppliesWithoutList(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	p, err := newPlugin(map[string]any{"quality": "1080p+"}, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	sp := p.(*seriesPlugin)

	low := makeEntry("Some.Show.S01E01.720p.HDTV", "http://x.com/a")
	if err := sp.filter(context.Background(), makeCtx(), low); err != nil {
		t.Fatal(err)
	}
	if !low.IsRejected() {
		t.Errorf("720p should be rejected when spec is 1080p+, got %v", low.State)
	}

	high := makeEntry("Some.Show.S02E03.1080p.WEB-DL", "http://x.com/b")
	if err := sp.filter(context.Background(), makeCtx(), high); err != nil {
		t.Fatal(err)
	}
	if !high.IsAccepted() {
		t.Errorf("1080p should be accepted when spec is 1080p+; got %v: %s", high.State, high.RejectReason)
	}
}

// --- follow tracking mode ---

func TestFollowAcceptsAllOnFirstEncounter(t *testing.T) {
	// No tracker entries yet — all episodes should be accepted (binge dump).
	p := openPlugin(t, map[string]any{"tracking": "follow"})
	tc := makeCtx()
	for _, title := range []string{
		"My.Show.S01E01.720p", "My.Show.S01E05.720p", "My.Show.S01E10.720p",
	} {
		e := makeEntry(title, "http://x.com/"+title)
		p.filter(context.Background(), tc, e)
		if !e.IsAccepted() {
			t.Errorf("first encounter: %s should be accepted, got: %s", title, e.RejectReason)
		}
	}
}

func TestFollowAcceptsNewSeasonInOnePass(t *testing.T) {
	// S01 is tracked. S02 drops all at once — all S02 episodes should be accepted.
	p := openPlugin(t, map[string]any{"tracking": "follow"})
	tc := makeCtx()

	// Establish S01 anchor.
	e1 := makeEntry("My.Show.S01E01.720p", "http://x.com/1")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	// Full S02 binge dump — all should be accepted in one pass.
	for _, title := range []string{
		"My.Show.S02E01.720p", "My.Show.S02E05.720p", "My.Show.S02E10.720p",
	} {
		e := makeEntry(title, "http://x.com/"+title)
		p.filter(context.Background(), tc, e)
		if !e.IsAccepted() {
			t.Errorf("S02 binge dump: %s should be accepted, got: %s", title, e.RejectReason)
		}
	}
}

func TestFollowRejectsEpisodesBeforeAnchorSeason(t *testing.T) {
	// S02 is the anchor. Old S01 episodes surfacing in a future feed run should be rejected.
	p := openPlugin(t, map[string]any{"tracking": "follow"})
	tc := makeCtx()

	// Establish S02 as anchor (started tracking mid-series).
	e1 := makeEntry("My.Show.S02E01.720p", "http://x.com/1")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	// S01 episode surfaces — should be rejected as it predates our tracking start.
	e2 := makeEntry("My.Show.S01E05.720p", "http://x.com/2")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("S01 episode should be rejected when tracking started at S02")
	}
}

func TestFollowAcceptsGapFillWithinAnchorSeason(t *testing.T) {
	// S01E01 downloaded. S01E03 then S01E02 arrive in subsequent runs — both accepted.
	p := openPlugin(t, map[string]any{"tracking": "follow"})
	tc := makeCtx()

	e1 := makeEntry("My.Show.S01E01.720p", "http://x.com/1")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	for _, title := range []string{"My.Show.S01E03.720p", "My.Show.S01E02.720p"} {
		e := makeEntry(title, "http://x.com/"+title)
		p.filter(context.Background(), tc, e)
		if !e.IsAccepted() {
			t.Errorf("gap fill %s should be accepted in follow mode, got: %s", title, e.RejectReason)
		}
	}
}

func TestFollowRejectsStaleOldSeasonWithNewerTracking(t *testing.T) {
	// Regression: a stale migrated record at S01 (real timestamp) must not
	// pull the tracking floor back to season 1 when S05 episodes are also
	// tracked (even with zero timestamps from a now-fixed bug window).
	p := openPlugin(t, map[string]any{"tracking": "follow"})

	if err := p.tracker.Mark(series.Record{
		SeriesName:   "my show",
		EpisodeID:    "S01E01",
		DownloadedAt: time.Now().Add(-100 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.tracker.Mark(series.Record{
		SeriesName:   "my show",
		EpisodeID:    "S05E08",
		DownloadedAt: time.Time{},
	}); err != nil {
		t.Fatal(err)
	}

	tc := makeCtx()
	e := makeEntry("My.Show.S01E04.720p", "http://x.com/1")
	p.filter(context.Background(), tc, e)
	if !e.IsRejected() {
		t.Error("S01E04 should be rejected: highest tracked is S05E08, so S01 predates the tracking window")
	}
}

// TestProcess_DoesNotPersist verifies that Process() does NOT write to the tracker.
// Only Commit() should persist.
func TestProcess_DoesNotPersist(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	out, err := p.Process(context.Background(), tc, []*entry.Entry{e})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !out[0].IsAccepted() {
		t.Fatalf("expected 1 accepted entry, got %v", out)
	}

	// Process must NOT have written to the tracker.
	// A second episode should still be accepted because S01E01 is not tracked.
	e2 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if e2.IsRejected() {
		t.Error("Process() must not persist to the tracker; S01E01 should not be tracked yet")
	}
}

// TestCommit_Persists verifies that Commit() writes to the episode tracker.
func TestCommit_Persists(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	// Process to accept and populate episode info.
	if _, err := p.Process(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	// Now commit.
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// After Commit, the same episode should be rejected (already tracked).
	e2 := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("Commit() should persist to the tracker; S01E01 should now be tracked")
	}
}

func TestDoubleEpisodeCommitMarksBothParts(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Accept and commit a double episode S01E01E02.
	e := makeEntry("My.Show.S01E01E02.1080p.WEB-DL", "http://x.com/double")
	if _, err := p.Process(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// S01E01 individually should now be rejected (already part of the downloaded double).
	e1 := makeEntry("My.Show.S01E01.1080p.WEB-DL", "http://x.com/e01")
	p.filter(context.Background(), tc, e1)
	if !e1.IsRejected() {
		t.Error("S01E01 should be rejected after double S01E01E02 was committed")
	}

	// S01E02 individually should also be rejected.
	e2 := makeEntry("My.Show.S01E02.1080p.WEB-DL", "http://x.com/e02")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("S01E02 should be rejected after double S01E01E02 was committed")
	}
}

func TestDoubleEpisodeDoesNotBlockNewContent(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Commit S01E01 individually.
	e := makeEntry("My.Show.S01E01.720p.WEB-DL", "http://x.com/e01")
	if _, err := p.Process(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// S01E01E02 double pack should still be accepted — it contains S01E02 which is new.
	edbl := makeEntry("My.Show.S01E01E02.1080p.WEB-DL", "http://x.com/double")
	p.filter(context.Background(), tc, edbl)
	if edbl.IsRejected() {
		t.Error("double S01E01E02 should not be blocked just because S01E01 was seen — S01E02 is new content")
	}
}

// TestProcessStampsMediaTypeSeries verifies that every entry the filter
// processes — accepted or rejected — has media_type=series stamped on it so
// downstream classifiers can rely on it as Certain.
func TestProcessStampsMediaTypeSeries(t *testing.T) {
	p := openPlugin(t, nil)
	accepted := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	rejected := makeEntry("Other.Show.S01E01.720p.HDTV", "http://x.com/b")

	in := []*entry.Entry{accepted, rejected}
	if _, err := p.Process(context.Background(), makeCtx(), in); err != nil {
		t.Fatal(err)
	}
	// PassThrough drops rejected entries from the result, but the underlying
	// entries are mutated in place — both still carry media_type.
	for _, e := range in {
		if got := e.GetString(entry.FieldMediaType); got != entry.MediaTypeSeries {
			t.Errorf("entry %q: media_type = %q, want %q", e.Title, got, entry.MediaTypeSeries)
		}
	}
}

// TestNoListTrackerDedups verifies that without a curated list, the tracker
// still dedups across runs — same episode is rejected on the second sighting.
// This is the headline benefit of the no-list mode: broad acceptance with
// tracking intact.
func TestNoListTrackerDedups(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pp, err := newPlugin(map[string]any{}, db)
	if err != nil {
		t.Fatal(err)
	}
	p := pp.(*seriesPlugin)
	tc := makeCtx()

	first := makeEntry("Random.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, first)
	p.persist(context.Background(), tc, []*entry.Entry{first})
	if !first.IsAccepted() {
		t.Fatalf("first sighting should be accepted; got %v", first.State)
	}

	second := makeEntry("Random.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, second)
	if !second.IsRejected() {
		t.Errorf("second sighting should be rejected by tracker; got %v", second.State)
	}
}
