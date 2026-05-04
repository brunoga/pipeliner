package movies

import (
	"context"
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

func openPlugin(t *testing.T, extra map[string]any) *moviesPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := map[string]any{
		"movies": []any{"Inception"},
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
	if err := p.Filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("expected accepted, got %v: %s", e.State, e.RejectReason)
	}
}

func TestFilterRejectsUnlistedMovie(t *testing.T) {
	p := openPlugin(t, map[string]any{"movies": []any{"The Matrix"}})
	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.Filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.IsRejected() {
		t.Errorf("unlisted movie should be left undecided, not rejected")
	}
}

func TestFilterNoYear(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("Inception.BluRay.1080p", "http://x.com/a")
	if err := p.Filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("no-year title should be left undecided")
	}
}

func TestFilterQualityGate(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "1080p"})
	e := entry.New("Inception.2010.720p.HDTV", "http://x.com/a")
	if err := p.Filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Errorf("720p should be rejected by 1080p quality floor")
	}
}

func TestFilterQualityGatePass(t *testing.T) {
	p := openPlugin(t, map[string]any{"quality": "720p"})
	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	if err := p.Filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Errorf("1080p should pass a 720p quality floor: %s", e.RejectReason)
	}
}

func TestDuplicateRejected(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e1 := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	p.Filter(context.Background(), tc, e1)  //nolint:errcheck
	e1.Accept()
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e2 := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("already-downloaded movie should be rejected")
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
	if d.PluginPhase != plugin.PhaseFilter {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}

func TestLearnMarksAccepted(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx()

	e := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/a")
	e.Accept()

	if err := p.Learn(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// Verify the entry is now seen.
	e2 := entry.New("Inception.2010.1080p.BluRay.x264", "http://x.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
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
		from:      []plugin.InputPlugin{mock},
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
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
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
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("unlisted movie should be undecided; got %v", e.State)
	}
}

func TestFromCachesResults(t *testing.T) {
	callCount := 0
	mock := &mockInput{}
	counted := &countingInput{wrapped: mock, count: &callCount}
	db, _ := store.OpenSQLite(":memory:")
	p := &moviesPlugin{
		from:      []plugin.InputPlugin{counted},
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

func TestMissingMoviesAndFrom(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{}, db)
	if err == nil {
		t.Fatal("expected error when neither movies nor from is configured")
	}
}
