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

// mockInput is a trivial InputPlugin returning a fixed list of entries.
type mockInput struct {
	entries []*entry.Entry
}

func (m *mockInput) Name() string        { return "mock_input" }
func (m *mockInput) Phase() plugin.Phase { return plugin.PhaseInput }
func (m *mockInput) Run(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
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
		"shows": []any{"My Show"},
	}
	maps.Copy(cfg, extra)
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*seriesPlugin)
}

// --- Basic accept/reject ---

func TestAcceptsKnownShow(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsAccepted() {
		t.Errorf("known show episode should be accepted: %s", e.RejectReason)
	}
}

func TestIgnoresUnknownShow(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("Other.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsAccepted() || e.IsRejected() {
		t.Error("unknown show should be left Undecided")
	}
}

func TestIgnoresNonEpisode(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("Just A Movie 2023 1080p BluRay", "http://x.com/a")
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsAccepted() || e.IsRejected() {
		t.Error("non-episode title should be left Undecided")
	}
}

// --- Seen deduplication ---

func TestDuplicateRejected(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()
	e1 := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), tc, e1) //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e2 := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("already-downloaded episode should be rejected")
	}
}

// --- Proper/repack upgrade ---

func TestProperUpgradesExisting(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Download a 720p copy.
	e1 := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), tc, e1) //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	// A PROPER 1080p copy should be accepted as an upgrade.
	e2 := entry.New("My.Show.S01E01.PROPER.1080p.BluRay", "http://x.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsAccepted() {
		t.Errorf("proper higher-quality copy should be accepted: %s", e2.RejectReason)
	}
}

func TestProperSameQualityNotUpgraded(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("My.Show.S01E01.720p.BluRay", "http://x.com/a")
	p.Filter(context.Background(), tc, e1) //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	// Same quality proper — should still be rejected (not a real upgrade).
	e2 := entry.New("My.Show.S01E01.PROPER.720p.HDTV", "http://x.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("proper with lower/equal quality should not replace existing")
	}
}

// --- Strict tracking ---

func TestStrictAllowsNext(t *testing.T) {
	p := openPlugin(t, map[string]any{"tracking": "strict"})
	tc := makeCtx()

	e1 := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), tc, e1) //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e2 := entry.New("My.Show.S01E02.720p.HDTV", "http://x.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsAccepted() {
		t.Errorf("next episode should be accepted in strict mode: %s", e2.RejectReason)
	}
}

func TestStrictRejectsSkip(t *testing.T) {
	p := openPlugin(t, map[string]any{"tracking": "strict"})
	tc := makeCtx()

	e1 := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), tc, e1) //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e3 := entry.New("My.Show.S01E04.720p.HDTV", "http://x.com/c")
	p.Filter(context.Background(), tc, e3) //nolint:errcheck
	if !e3.IsRejected() {
		t.Error("strict mode should reject an episode that skips ahead by 3")
	}
}

func TestBackfillAllowsOlder(t *testing.T) {
	p := openPlugin(t, map[string]any{"tracking": "backfill"})
	tc := makeCtx()

	e5 := entry.New("My.Show.S01E05.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), tc, e5) //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e5}) //nolint:errcheck

	e1 := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.Filter(context.Background(), tc, e1) //nolint:errcheck
	if !e1.IsAccepted() {
		t.Errorf("backfill mode should accept older episodes: %s", e1.RejectReason)
	}
}

// --- Quality filter ---

func TestQualityGateRejects(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "720p"})
	e := entry.New("My.Show.S01E01.480p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("quality gate should reject 480p when spec is 720p")
	}
}

func TestQualityGateAccepts(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "720p"})
	e := entry.New("My.Show.S01E01.1080p.BluRay", "http://x.com/a")
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsAccepted() {
		t.Errorf("1080p should pass 720p quality gate: %s", e.RejectReason)
	}
}

// --- Fuzzy matching ---

func TestFuzzyMatchTypo(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"my show", "my show", true},
		{"my show", "my sho", true},           // 1 edit (drop w)
		{"my show", "mi show", true},          // 1 edit (y→i)
		{"completely different", "my show", false},
		{"my show", "my show 2", false},       // 2 edits — different show
		{"my show", "my show extra", false},   // 5 edits — clearly different
	}
	for _, tc := range cases {
		got := match.Fuzzy(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("match.Fuzzy(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// --- Entry fields ---

func TestEpisodeFieldsSet(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("My.Show.S02E05.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck

	if v := e.GetString("series_episode_id"); v != "S02E05" {
		t.Errorf("episode_id: got %q, want S02E05", v)
	}
}

// --- Config validation ---

func TestMissingShows(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{}, db)
	if err == nil {
		t.Error("expected error when shows list is empty")
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
	if d.PluginPhase != plugin.PhaseFilter {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}

// --- Dynamic from ---

func openWithFrom(t *testing.T, mock *mockInput) *seriesPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return &seriesPlugin{
		from:      []plugin.InputPlugin{mock},
		listCache: cache.NewPersistent[[]string](time.Hour, db.Bucket("test")),
		tracking:  trackingBackfill,
		tracker:   series.NewTracker(db.Bucket("series")),
	}
}

func TestFromAcceptsDynamicShow(t *testing.T) {
	mock := &mockInput{entries: []*entry.Entry{
		entry.New("My Dynamic Show", ""),
	}}
	p := openWithFrom(t, mock)

	e := entry.New("My.Dynamic.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsAccepted() {
		t.Errorf("dynamic show should be accepted: %s", e.RejectReason)
	}
}

func TestFromIgnoresUnlistedShow(t *testing.T) {
	mock := &mockInput{entries: []*entry.Entry{
		entry.New("My Dynamic Show", ""),
	}}
	p := openWithFrom(t, mock)

	e := entry.New("Other.Show.S01E01.720p.HDTV", "http://x.com/a")
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("unlisted show should be undecided; got %v", e.State)
	}
}

func TestFromCachesResults(t *testing.T) {
	callCount := 0
	mock := &mockInput{}
	// Use a wrapper so we can count calls.
	counted := &countingInput{wrapped: mock, count: &callCount}
	db, _ := store.OpenSQLite(":memory:")
	p := &seriesPlugin{
		from:      []plugin.InputPlugin{counted},
		listCache: cache.NewPersistent[[]string](time.Hour, db.Bucket("test")),
		tracking:  trackingBackfill,
		tracker:   series.NewTracker(db.Bucket("series")),
	}
	tc := makeCtx()
	p.resolveShows(context.Background(), tc)
	p.resolveShows(context.Background(), tc)
	if callCount != 1 {
		t.Errorf("from input should be called once due to caching; got %d calls", callCount)
	}
}

type countingInput struct {
	wrapped *mockInput
	count   *int
}

func (c *countingInput) Name() string        { return "counting_input" }
func (c *countingInput) Phase() plugin.Phase { return plugin.PhaseInput }
func (c *countingInput) Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	*c.count++
	return c.wrapped.Run(ctx, tc)
}

func TestMultipleEntriesSameEpisodeAllAccepted(t *testing.T) {
	// Multiple entries for the same episode (different quality/source) should all
	// be accepted by series.Filter so the dedup step can pick the best one.
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	e2 := entry.New("My.Show.S01E01.1080p.WEB-DL", "http://x.com/b")
	e3 := entry.New("My.Show.S01E01.2160p.BluRay", "http://x.com/c")

	for _, e := range []*entry.Entry{e1, e2, e3} {
		p.Filter(context.Background(), tc, e) //nolint:errcheck
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

	e1 := entry.New("My.Show.S01E01.1080p.WEB-DL", "http://x.com/a")
	p.Filter(context.Background(), tc, e1) //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e2 := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("episode already in tracker should be rejected on next run")
	}
}

func TestMissingShowsAndFrom(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{}, db)
	if err == nil {
		t.Error("expected error when neither shows nor from is configured")
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
		e := entry.New(title, "http://x.com/"+title)
		p.Filter(context.Background(), tc, e) //nolint:errcheck
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
	e1 := entry.New("My.Show.S01E01.720p", "http://x.com/1")
	p.Filter(context.Background(), tc, e1)         //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	// Full S02 binge dump — all should be accepted in one pass.
	for _, title := range []string{
		"My.Show.S02E01.720p", "My.Show.S02E05.720p", "My.Show.S02E10.720p",
	} {
		e := entry.New(title, "http://x.com/"+title)
		p.Filter(context.Background(), tc, e) //nolint:errcheck
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
	e1 := entry.New("My.Show.S02E01.720p", "http://x.com/1")
	p.Filter(context.Background(), tc, e1)         //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	// S01 episode surfaces — should be rejected as it predates our tracking start.
	e2 := entry.New("My.Show.S01E05.720p", "http://x.com/2")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("S01 episode should be rejected when tracking started at S02")
	}
}

func TestFollowAcceptsGapFillWithinAnchorSeason(t *testing.T) {
	// S01E01 downloaded. S01E03 then S01E02 arrive in subsequent runs — both accepted.
	p := openPlugin(t, map[string]any{"tracking": "follow"})
	tc := makeCtx()

	e1 := entry.New("My.Show.S01E01.720p", "http://x.com/1")
	p.Filter(context.Background(), tc, e1)         //nolint:errcheck
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	for _, title := range []string{"My.Show.S01E03.720p", "My.Show.S01E02.720p"} {
		e := entry.New(title, "http://x.com/"+title)
		p.Filter(context.Background(), tc, e) //nolint:errcheck
		if !e.IsAccepted() {
			t.Errorf("gap fill %s should be accepted in follow mode, got: %s", title, e.RejectReason)
		}
	}
}
