package movies

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
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

// metaize simulates what metainfo_file does upstream: classifies the entry as
// a movie and populates the title / video_year / video_is_3d / video_proper /
// video_repack / _quality fields the movies filter requires. The movies
// plugin requires these (via Descriptor.Requires) so tests must call this
// helper before invoking filter() or Process().
func metaize(e *entry.Entry) {
	if m, ok := imovies.Parse(e.Title); ok {
		setMovieFields(e, m)
	} else if y := entry.ReleaseYear(e); y > 0 {
		// List-source fallback: a clean title (no year, no quality) with
		// video_year already set upstream.
		title := imovies.NormalizeTitle(e.Title)
		if title == "" {
			title = e.Title
		}
		setMovieFields(e, &imovies.Movie{Title: title, Year: y})
	}
	q := quality.Parse(e.Title)
	if q != (quality.Quality{}) {
		e.SetQuality(q)
		e.SetVideoInfo(entry.VideoInfo{
			Quality:    q.String(),
			Resolution: q.ResolutionName(),
			Source:     q.SourceName(),
			Is3D:       q.Format3D != quality.Format3DNone,
		})
	}
}

func setMovieFields(e *entry.Entry, m *imovies.Movie) {
	e.SetMovieInfo(entry.MovieInfo{
		VideoInfo: entry.VideoInfo{
			GenericInfo: entry.GenericInfo{Title: m.Title},
			Year:        m.Year,
			Proper:      m.Proper,
			Repack:      m.Repack,
		},
	})
}

// makeEntry builds an entry with the given title and runs metaize so the
// fields movies reads are present (simulating metainfo_file upstream).
func makeEntry(title, url string) *entry.Entry {
	e := entry.New(title, url)
	metaize(e)
	return e
}

func TestFilterAcceptsListedMovie(t *testing.T) {
	p := openPlugin(t, nil)
	e := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("expected accepted, got %v: %s", e.State, e.RejectReason)
	}
}

func TestFilterRejectsUnlistedMovieByDefault(t *testing.T) {
	p := openPlugin(t, map[string]any{"static": []any{"The Matrix"}})
	e := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
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
	e := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.IsRejected() {
		t.Error("unlisted movie should be left undecided when reject_unmatched is false")
	}
}

func TestFilterNoYear(t *testing.T) {
	// A title with no year but a quality marker still parses and matches.
	p := openPlugin(t, nil)
	e := makeEntry("Inception.BluRay.1080p", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("year-less title with quality marker should be accepted when it matches the list")
	}
}

func TestFilterNoQualityMarker(t *testing.T) {
	// A title with no year and no quality marker — metainfo_file can't
	// classify it as a movie, so the movies filter rejects by default.
	p := openPlugin(t, nil)
	e := makeEntry("Inception something with no quality markers at all", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Errorf("unparseable title should be rejected by default")
	}
}

func TestFilterNoQualityMarkerOptOut(t *testing.T) {
	p := openPlugin(t, map[string]any{"reject_unmatched": false})
	e := makeEntry("Inception something with no quality markers at all", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("unparseable title should be left undecided when reject_unmatched is false")
	}
}

func TestFilterQualityGate(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "1080p"})
	e := makeEntry("Inception.2010.720p.HDTV", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Errorf("720p should be rejected by 1080p quality floor")
	}
}

func TestFilterQualityGatePass(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "720p+"})
	e := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("1080p should pass a 720p+ quality floor: %s", e.RejectReason)
	}
}

func TestFilterQualityGate3DRejectsNon3D(t *testing.T) {
	p := openPlugin(t, map[string]any{
		"static":  []any{"Despicable Me 3"},
		"quality": "3dfull",
	})
	e := makeEntry("Despicable Me 3 Bluray Complete d666", "http://x.com/a")
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
	e := makeEntry("Inception.2010.FSBS.1080p.BluRay", "http://x.com/a")
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

	e1 := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("already-downloaded movie should be rejected")
	}
}

func TestQualityUpgradesExisting(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("Inception.2010.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("higher-quality copy should be accepted: %s", e2.RejectReason)
	}
}

func TestProperSameQualityUpgradesExisting(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("Inception.2010.720p.HDTV", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := makeEntry("Inception.2010.PROPER.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Errorf("proper at same quality should be accepted: %s", e2.RejectReason)
	}
}

func TestRepackNotAcceptedAgainAfterRepackDownloaded(t *testing.T) {
	// Regression: once a REPACK is downloaded, the same REPACK torrent must not
	// be accepted on every subsequent pipeline run (infinite loop).
	p := openPlugin(t, nil)
	tc := makeCtx()

	// Step 1: download the original, then the REPACK (first upgrade, should accept).
	e1 := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := makeEntry("Inception.2010.REPACK.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsAccepted() {
		t.Fatalf("REPACK of non-REPACK should be accepted: %s", e2.RejectReason)
	}
	p.persist(context.Background(), tc, []*entry.Entry{e2})

	// Step 2: same REPACK appears again on the next run — must be rejected.
	e3 := makeEntry("Inception.2010.REPACK.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, e3)
	if !e3.IsRejected() {
		t.Error("same REPACK must not be accepted again after it was already downloaded")
	}
}

func TestNoUpgradeWhenQualityNotBetter(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("Inception.2010.720p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), tc, e1)
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	// Lower source quality (HDTV < BluRay) — rejected even with PROPER tag.
	e2 := makeEntry("Inception.2010.PROPER.720p.HDTV", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("copy with lower quality should not replace existing")
	}
}

// TestEmptyConfigAcceptsAll documents the no-list path: a movies filter with
// no static and no list is valid and behaves as an accept-all filter (gated
// only by the upstream Requires, the quality spec, and the tracker).
func TestEmptyConfigAcceptsAll(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	p, err := newPlugin(map[string]any{}, db)
	if err != nil {
		t.Fatalf("empty config should be valid for accept-all mode: %v", err)
	}
	mp := p.(*moviesPlugin)
	if mp.hasList() {
		t.Error("hasList should be false with no static or list")
	}
	e := makeEntry("Never.Listed.Movie.2024.1080p.BluRay.x264", "http://x.com/a")
	if err := mp.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("accept-all filter should accept any classified movie; got %v: %s", e.State, e.RejectReason)
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

// TestRequiresDeclared verifies that the plugin descriptor declares the
// required upstream fields. The DAG validator uses this to catch pipelines
// that wire movies without an upstream metainfo step.
func TestRequiresDeclared(t *testing.T) {
	d, ok := plugin.Lookup("movies")
	if !ok {
		t.Fatal("movies not registered")
	}
	want := []string{
		entry.FieldTitle,
		entry.FieldVideoYear,
		entry.FieldQuality,
	}
	flat := make([]string, 0)
	for _, group := range d.Requires {
		flat = append(flat, group...)
	}
	for _, f := range want {
		if !slices.Contains(flat, f) {
			t.Errorf("Requires must include %q", f)
		}
	}
}

func TestLearnMarksAccepted(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	e.Set(moviesTrackerName, "inception")
	e.Accept()

	if err := p.persist(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// Verify the entry is now seen.
	e2 := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("movie should be rejected after learn marks it seen")
	}
}

func TestFuzzyMatch(t *testing.T) {
	if !match.Fuzzy("the dark knight", "the dark knight") {
		t.Error("exact match failed")
	}
	if match.Fuzzy("the dark knigt", "the dark knight") {
		t.Error("single typo must not match — silent wrong-matches outweigh typo tolerance")
	}
	if match.Fuzzy("the dark knight 2", "the dark knight") {
		t.Error("sequel should not match")
	}
}

// --- Dynamic list ---

func openWithFrom(t *testing.T, mock *mockInput) *moviesPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return &moviesPlugin{
		listSources: []plugin.SourcePlugin{mock},
		listCache:   cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("test")),
		tracker:     imovies.NewTracker(db.Bucket("movies")),
	}
}

func TestFromAcceptsDynamicMovie(t *testing.T) {
	mock := &mockInput{entries: []*entry.Entry{
		entry.New("Inception", ""),
	}}
	p := openWithFrom(t, mock)

	e := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("dynamic movie should be accepted: %s", e.RejectReason)
	}
}

func TestFromIgnoresUnlistedMovie(t *testing.T) {
	mock := &mockInput{entries: []*entry.Entry{
		entry.New("Inception", ""),
	}}
	p := openWithFrom(t, mock)

	e := makeEntry("The.Matrix.1999.1080p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), makeCtx(), e)
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
		listSources: []plugin.SourcePlugin{counted},
		listCache:   cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("test")),
		tracker:     imovies.NewTracker(db.Bucket("movies")),
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
		listSources: []plugin.SourcePlugin{counted},
		listCache:   cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("test")),
		tracker:     imovies.NewTracker(db.Bucket("movies")),
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

func (c *countingInput) Name() string { return "counting_input" }
func (c *countingInput) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	*c.count++
	return c.wrapped.Generate(ctx, tc)
}

func TestMultipleEntriesSameMovieAllAccepted(t *testing.T) {
	// Multiple entries for the same movie (different quality/source) should all
	// be accepted by movies.Filter so the dedup step can pick the best one.
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := makeEntry("Inception.2010.720p.HDTV", "http://x.com/a")
	e2 := makeEntry("Inception.2010.1080p.WEB-DL", "http://x.com/b")
	e3 := makeEntry("Inception.2010.2160p.BluRay", "http://x.com/c")

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

	e1 := makeEntry("Inception.2010.1080p.WEB-DL", "http://x.com/a")
	if err := p.filter(context.Background(), tc, e1); err != nil {
		t.Fatal(err)
	}
	if err := p.persist(context.Background(), tc, []*entry.Entry{e1}); err != nil {
		t.Fatal(err)
	}

	e2 := makeEntry("Inception.2010.720p.HDTV", "http://x.com/b")
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsRejected() {
		t.Error("already-downloaded movie should be rejected on next run")
	}
}

func TestProcess_DoesNotPersist(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if _, err := p.Process(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// Process must not write to the tracker — a second entry for the same
	// movie must still be accepted (no duplicate rejection).
	e2 := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if e2.IsRejected() {
		t.Error("Process() must not persist; movie should not be seen after Process alone")
	}
}

func TestCommit_Persists(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	// Run Process so the matched-title tracker field is stamped.
	if _, err := p.Process(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// After Commit, the movie should be marked as seen.
	e2 := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsRejected() {
		t.Error("movie should be rejected after Commit marks it seen")
	}
}

// TestQualitySpecAppliesWithoutList exercises the no-list path with a
// quality spec: the spec is still an absolute gate, so a movie below it
// gets rejected even when there's no title list to match against.
func TestQualitySpecAppliesWithoutList(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	p, err := newPlugin(map[string]any{"quality": "1080p+"}, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	mp := p.(*moviesPlugin)

	low := makeEntry("Cool.Movie.2024.720p.WEB-DL", "http://x.com/a")
	if err := mp.filter(context.Background(), makeCtx(), low); err != nil {
		t.Fatal(err)
	}
	if !low.IsRejected() {
		t.Errorf("720p should be rejected when spec is 1080p+, got %v", low.State)
	}

	high := makeEntry("Other.Movie.2024.1080p.BluRay.x264", "http://x.com/b")
	if err := mp.filter(context.Background(), makeCtx(), high); err != nil {
		t.Fatal(err)
	}
	if !high.IsAccepted() {
		t.Errorf("1080p should be accepted when spec is 1080p+; got %v: %s", high.State, high.RejectReason)
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
	e1 := makeEntry("Ferrari.2023.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-FLUX", "http://x.com/1")
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
	e2 := makeEntry("Ferrari.2023.REPACK.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-FLUX", "http://x.com/2")
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

// TestProcessStampsMediaTypeMovie verifies that every entry the filter
// processes — accepted or rejected — has media_type=movie stamped on it,
// so downstream classifiers (dedup, route, condition) can rely on it.
func TestProcessStampsMediaTypeMovie(t *testing.T) {
	p := openPlugin(t, map[string]any{"static": []any{"Inception"}})
	accepted := makeEntry("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	rejected := makeEntry("Unknown.Title.2010.1080p.BluRay", "http://x.com/b")
	in := []*entry.Entry{accepted, rejected}

	if _, err := p.Process(context.Background(), makeCtx(), in); err != nil {
		t.Fatal(err)
	}
	// PassThrough drops the rejected entry from the result, but the
	// underlying entries are mutated in place — both still carry media_type.
	for _, e := range in {
		if got := e.GetString(entry.FieldMediaType); got != entry.MediaTypeMovie {
			t.Errorf("entry %q: media_type = %q, want %q", e.Title, got, entry.MediaTypeMovie)
		}
	}
}

// TestNoListTrackerDedups mirrors the series test: without a curated list,
// the tracker still dedups across runs.
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
	p := pp.(*moviesPlugin)
	tc := makeCtx()

	first := makeEntry("Random.Movie.2024.1080p.BluRay.x264", "http://x.com/a")
	p.filter(context.Background(), tc, first)
	p.persist(context.Background(), tc, []*entry.Entry{first})
	if !first.IsAccepted() {
		t.Fatalf("first sighting should be accepted; got %v", first.State)
	}

	second := makeEntry("Random.Movie.2024.1080p.BluRay.x264", "http://x.com/b")
	p.filter(context.Background(), tc, second)
	if !second.IsRejected() {
		t.Errorf("second sighting should be rejected by tracker; got %v", second.State)
	}
}
