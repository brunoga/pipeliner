package series

import (
	"context"
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

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func openPlugin(t *testing.T, extra map[string]any) *seriesPlugin {
	t.Helper()
	cfg := map[string]any{
		"shows": []any{"My Show"},
		"db":    ":memory:",
	}
	maps.Copy(cfg, extra)
	p, err := newPlugin(cfg)
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

	if v := e.GetString("series_name"); v == "" {
		t.Error("series_name should be set")
	}
	if v := e.GetString("series_episode_id"); v != "S02E05" {
		t.Errorf("series_episode_id: got %q, want S02E05", v)
	}
}

// --- Config validation ---

func TestMissingShows(t *testing.T) {
	_, err := newPlugin(map[string]any{"db": "/tmp/x.db"})
	if err == nil {
		t.Error("expected error when shows list is empty")
	}
}

func TestUnknownTrackingMode(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"shows":    []any{"My Show"},
		"tracking": "invalid",
		"db":       "/tmp/x.db",
	})
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
		tracking:  trackingAll,
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
		tracking:  trackingAll,
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

func TestMissingShowsAndFrom(t *testing.T) {
	_, err := newPlugin(map[string]any{"db": ":memory:"})
	if err == nil {
		t.Error("expected error when neither shows nor from is configured")
	}
}
