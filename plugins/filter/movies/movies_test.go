package movies

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// mockInput is a SourcePlugin returning a fixed list of entries.
type mockInput struct {
	entries []*entry.Entry
}

func (m *mockInput) Name() string        { return "mock_input" }
func (m *mockInput) Phase() plugin.Phase { return plugin.PhaseFrom }
func (m *mockInput) Generate(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	return m.entries, nil
}

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func openPlugin(t *testing.T, extra map[string]any) *moviesPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := map[string]any{
		"static": []any{"Inception"},
	}
	maps.Copy(cfg, extra)
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*moviesPlugin)
}

func TestFilterAcceptsListedMovie(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("expected accepted, got %v: %s", e.State, e.RejectReason)
	}
}

func TestFilterRejectsUnlistedMovieByDefault(t *testing.T) {
	p := openPlugin(t, map[string]any{"static": []any{"The Matrix"}})
	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Error("unlisted movie should be rejected by default")
	}
}

func TestFilterUnmatchedOptOut(t *testing.T) {
	p := openPlugin(t, map[string]any{
		"static":           []any{"The Matrix"},
		"reject_unmatched": false,
	})
	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.IsRejected() {
		t.Error("unlisted movie should be left undecided when reject_unmatched is false")
	}
}

func TestFilterNoYear(t *testing.T) {
	// A title with no year but a quality marker now parses and can match.
	p := openPlugin(t, nil)
	e := entry.New("Inception.BluRay.1080p", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("year-less title with quality marker should be accepted when it matches the list")
	}
}

func TestFilterNoQualityMarker(t *testing.T) {
	// A title with no year and no quality marker cannot be parsed — rejected by default.
	p := openPlugin(t, nil)
	e := entry.New("Inception something with no quality markers at all", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Errorf("unparseable title should be rejected by default")
	}
}

func TestFilterNoQualityMarkerOptOut(t *testing.T) {
	p := openPlugin(t, map[string]any{"reject_unmatched": false})
	e := entry.New("Inception something with no quality markers at all", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("unparseable title should be left undecided when reject_unmatched is false")
	}
}

func TestFilterQualityGate(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "1080p"})
	e := entry.New("Inception.2010.720p.HDTV", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Errorf("720p should be rejected by 1080p quality floor")
	}
}

func TestFilterQualityGatePass(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "720p"})
	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("1080p should pass a 720p quality floor: %s", e.RejectReason)
	}
}

func TestFilterQualityGate3DRejectsNon3D(t *testing.T) {
	p := openPlugin(t, map[string]any{
		"static":  []any{"Despicable Me 3"},
		"quality": "3dfull",
	})
	e := entry.New("Despicable Me 3 Bluray Complete d666", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Errorf("non-3D entry should be rejected by 3dfull quality gate, got state=%v", e.State)
	}
}

func TestFilterQualityGate3DAccepts3DFull(t *testing.T) {
	p := openPlugin(t, map[string]any{
		"static":  []any{"Inception"},
		"quality": "3dfull",
	})
	e := entry.New("Inception.2010.SBS.1080p.BluRay", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("3D-Full entry should be accepted by 3dfull quality gate: %s", e.RejectReason)
	}
}

func TestDuplicateRejected(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), tc, e1)  //nolint:errcheck
	e1.Accept()
	p.persist(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e2 := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("already-downloaded movie should be rejected")
	}
}

func TestQualityUpgradesExisting(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("Inception.2010.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)  //nolint:errcheck
	e1.Accept()
	p.persist(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e2 := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsAccepted() {
		t.Errorf("higher-quality copy should be accepted: %s", e2.RejectReason)
	}
}

func TestProperSameQualityUpgradesExisting(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("Inception.2010.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)  //nolint:errcheck
	e1.Accept()
	p.persist(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e2 := entry.New("Inception.2010.PROPER.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsAccepted() {
		t.Errorf("proper at same quality should be accepted: %s", e2.RejectReason)
	}
}

func TestNoUpgradeWhenQualityNotBetter(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("Inception.2010.720p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), tc, e1)  //nolint:errcheck
	e1.Accept()
	p.persist(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	// Lower source quality (HDTV < BluRay) — rejected even with PROPER tag.
	e2 := entry.New("Inception.2010.PROPER.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("copy with lower quality should not replace existing")
	}
}

func TestRequiredMoviesList(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{}, db)
	if err == nil {
		t.Fatal("expected error when movies list is missing")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("movies")
	if !ok {
		t.Fatal("movies not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("phase: got %v", d.Role)
	}
}

func TestLearnMarksAccepted(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	e.Accept()

	if err := p.persist(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// Verify the entry is now seen.
	e2 := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("movie should be rejected after learn marks it seen")
	}
}

func TestFuzzyMatch(t *testing.T) {
	if !match.Fuzzy("the dark knight", "the dark knight") {
		t.Error("exact match failed")
	}
	if !match.Fuzzy("the dark knigt", "the dark knight") {
		t.Error("single typo should match")
	}
	if match.Fuzzy("the dark knight 2", "the dark knight") {
		t.Error("sequel should not match")
	}
}

// --- Dynamic from ---

func openWithFrom(t *testing.T, mock *mockInput) *moviesPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return &moviesPlugin{
		from:      []plugin.SourcePlugin{mock},
		listCache: cache.NewPersistent[[]string](time.Hour, db.Bucket("test")),
		tracker:   imovies.NewTracker(db.Bucket("movies")),
	}
}

func TestFromAcceptsDynamicMovie(t *testing.T) {
	mock := &mockInput{entries: []*entry.Entry{
		entry.New("Inception", ""),
	}}
	p := openWithFrom(t, mock)

	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsAccepted() {
		t.Errorf("dynamic movie should be accepted: %s", e.RejectReason)
	}
}

func TestFromIgnoresUnlistedMovie(t *testing.T) {
	mock := &mockInput{entries: []*entry.Entry{
		entry.New("Inception", ""),
	}}
	p := openWithFrom(t, mock)

	e := entry.New("The.Matrix.1999.1080p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("unlisted movie should be undecided; got %v", e.State)
	}
}

func TestFromCachesResults(t *testing.T) {
	callCount := 0
	mock := &mockInput{entries: []*entry.Entry{entry.New("Inception", "")}}
	counted := &countingInput{wrapped: mock, count: &callCount}
	db, _ := store.OpenSQLite(":memory:")
	p := &moviesPlugin{
		from:      []plugin.SourcePlugin{counted},
		listCache: cache.NewPersistent[[]string](time.Hour, db.Bucket("test")),
		tracker:   imovies.NewTracker(db.Bucket("movies")),
	}
	tc := makeCtx()
	p.resolveTitles(context.Background(), tc)
	p.resolveTitles(context.Background(), tc)
	if callCount != 1 {
		t.Errorf("from input should be called once due to caching; got %d calls", callCount)
	}
}

func TestFromEmptyResultNotCached(t *testing.T) {
	callCount := 0
	mock := &mockInput{} // returns no entries
	counted := &countingInput{wrapped: mock, count: &callCount}
	db, _ := store.OpenSQLite(":memory:")
	p := &moviesPlugin{
		from:      []plugin.SourcePlugin{counted},
		listCache: cache.NewPersistent[[]string](time.Hour, db.Bucket("test")),
		tracker:   imovies.NewTracker(db.Bucket("movies")),
	}
	tc := makeCtx()
	p.resolveTitles(context.Background(), tc)
	p.resolveTitles(context.Background(), tc)
	if callCount != 2 {
		t.Errorf("empty from result should not be cached; plugin called %d times, want 2", callCount)
	}
}

type countingInput struct {
	wrapped *mockInput
	count   *int
}

func (c *countingInput) Name() string        { return "counting_input" }
func (c *countingInput) Phase() plugin.Phase { return plugin.PhaseFrom }
func (c *countingInput) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	*c.count++
	return c.wrapped.Generate(ctx, tc)
}

func TestFilterSetsQualityAndNot3D(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Fatalf("expected accepted: %s", e.RejectReason)
	}
	if e.GetString("video_quality") == "" {
		t.Error("movie_quality should be set")
	}
	if e.GetBool("video_is_3d") {
		t.Error("movie_3d should be false for non-3D title")
	}
}

func TestFilterSets3D(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("Inception.2010.3D.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Fatalf("expected accepted: %s", e.RejectReason)
	}
	if !e.GetBool("video_is_3d") {
		t.Error("movie_3d should be true for 3D title")
	}
}

func TestMultipleEntriesSameMovieAllAccepted(t *testing.T) {
	// Multiple entries for the same movie (different quality/source) should all
	// be accepted by movies.Filter so the dedup step can pick the best one.
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("Inception.2010.720p.HDTV", "http://x.com/a")
	e2 := entry.New("Inception.2010.1080p.WEB-DL", "http://x.com/b")
	e3 := entry.New("Inception.2010.2160p.BluRay", "http://x.com/c")

	for _, e := range []*entry.Entry{e1, e2, e3} {
		if err := p.filter(context.Background(), tc, e); err != nil {
			t.Fatal(err)
		}
	}

	for _, e := range []*entry.Entry{e1, e2, e3} {
		if !e.IsAccepted() {
			t.Errorf("entry %q should be accepted before dedup: %s", e.Title, e.RejectReason)
		}
	}
}

func TestMultipleEntriesSameMovieLearnRejectsOldOnNextRun(t *testing.T) {
	// After Learn persists one accepted entry, subsequent runs reject that movie.
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("Inception.2010.1080p.WEB-DL", "http://x.com/a")
	e1.Set("movie_title", "Inception")
	if err := p.filter(context.Background(), tc, e1); err != nil {
		t.Fatal(err)
	}
	if err := p.persist(context.Background(), tc, []*entry.Entry{e1}); err != nil {
		t.Fatal(err)
	}

	e2 := entry.New("Inception.2010.720p.HDTV", "http://x.com/b")
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsRejected() {
		t.Error("already-downloaded movie should be rejected on next run")
	}
}

func TestMissingMoviesAndFrom(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{}, db)
	if err == nil {
		t.Fatal("expected error when neither movies nor from is configured")
	}
}

func TestSpecCheckedBeforeIsSeen_3DTaskDoesNotAcceptNon3DRepack(t *testing.T) {
	// Regression: a non-3D film recorded by the 'movies' task (is3D=false) was
	// being accepted by the 'movies-3d' task as a REPACK upgrade because the
	// IsSeen lookup (using is3D=false) found the tracker record before the
	// quality spec check ran. The spec check must come first.

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tc := makeCtx()

	// Shared tracker bucket — both tasks use the same "movies" bucket.
	flatPlugin := openPluginWithDB(t, db, map[string]any{
		"static":  []any{"Ferrari"},
		"quality": "1080p+",
	})
	threeDPlugin := openPluginWithDB(t, db, map[string]any{
		"static":  []any{"Ferrari"},
		"quality": "3dfull",
	})

	// Step 1: flat movies task downloads Ferrari at 1080p WEB-DL (non-3D).
	e1 := entry.New("Ferrari.2023.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-FLUX", "http://x.com/1")
	if err := flatPlugin.filter(context.Background(), tc, e1); err != nil {
		t.Fatal(err)
	}
	if !e1.IsAccepted() {
		t.Fatalf("flat movies should accept Ferrari 1080p: %s", e1.RejectReason)
	}
	if err := flatPlugin.persist(context.Background(), tc, []*entry.Entry{e1}); err != nil {
		t.Fatal(err)
	}

	// Step 2: movies-3d task sees the REPACK version — must reject it because
	// it is NOT 3D, even though it's a REPACK of a previously downloaded title.
	e2 := entry.New("Ferrari.2023.REPACK.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-FLUX", "http://x.com/2")
	if err := threeDPlugin.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if e2.IsAccepted() {
		t.Error("movies-3d should reject non-3D Ferrari REPACK — quality spec must be checked before IsSeen upgrade path")
	}
}

func openPluginWithDB(t *testing.T, db *store.SQLiteStore, cfg map[string]any) *moviesPlugin {
	t.Helper()
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*moviesPlugin)
}
