package discover

import (
	"context"
	"log/slog"
	"maps"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// mockSearch is a SearchPlugin that returns pre-configured entries per query
// title. It also captures the full query entry per title so tests can assert
// on the hint fields discover forwarded.
type mockSearch struct {
	pluginName string
	results    map[string][]*entry.Entry
	calls      map[string]int
	queries    map[string]*entry.Entry
}

func newMockSearch(name string) *mockSearch {
	return &mockSearch{
		pluginName: name,
		results:    map[string][]*entry.Entry{},
		calls:      map[string]int{},
		queries:    map[string]*entry.Entry{},
	}
}

func (m *mockSearch) Name() string { return m.pluginName }
func (m *mockSearch) Search(_ context.Context, _ *plugin.TaskContext, qe *entry.Entry) ([]*entry.Entry, error) {
	m.calls[qe.Title]++
	m.queries[qe.Title] = qe
	return m.results[qe.Title], nil
}

// registerMock registers the mock under a unique name so discover can look it up.
func registerMock(mock *mockSearch) string {
	name := "mock-" + mock.pluginName
	func() {
		defer func() { recover() }() // tolerate duplicate registration across test runs
		plugin.Register(&plugin.Descriptor{
			PluginName: name,
			Role:       plugin.RoleSource,
			Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
				return mock, nil
			},
		})
	}()
	return name
}

func buildPlugin(t *testing.T, mock *mockSearch, titles []string, extraCfg map[string]any) *discoverPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	pluginName := registerMock(mock)
	cfg := map[string]any{
		"titles":   titlesToAny(titles),
		"search":   []any{pluginName},
		"interval": "1h",
	}
	maps.Copy(cfg, extraCfg)
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*discoverPlugin)
}

func titlesToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func taskCtx(name string) *plugin.TaskContext {
	return &plugin.TaskContext{Name: name, Logger: slog.Default()}
}

// run is a test helper that calls Process with no upstream entries (title-only mode).
func run(ctx context.Context, p *discoverPlugin, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return p.Process(ctx, tc, nil)
}

// --- tests ---

func TestDiscoverCollectsResults(t *testing.T) {
	mock := newMockSearch("collect")
	mock.results["Breaking Bad"] = []*entry.Entry{
		entry.New("Breaking.Bad.S01E01.720p", "http://example.com/1"),
		entry.New("Breaking.Bad.S01E02.720p", "http://example.com/2"),
	}

	p := buildPlugin(t, mock, []string{"Breaking Bad"}, nil)
	got, err := run(context.Background(), p, taskCtx("t"))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d entries, want 2", len(got))
	}
}

func TestDiscoverDeduplicatesByURL(t *testing.T) {
	mock := newMockSearch("dedup")
	e := entry.New("Dup.720p", "http://example.com/same")
	mock.results["Show"] = []*entry.Entry{e, e}

	p := buildPlugin(t, mock, []string{"Show"}, nil)
	got, err := run(context.Background(), p, taskCtx("t"))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d entries, want 1 (deduped)", len(got))
	}
}

func TestDiscoverIntervalPreventsResearch(t *testing.T) {
	mock := newMockSearch("interval")
	mock.results["Show"] = []*entry.Entry{
		entry.New("Show.S01E01", "http://example.com/1"),
	}

	p := buildPlugin(t, mock, []string{"Show"}, nil)

	if _, err := run(context.Background(), p, taskCtx("interval-task")); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if mock.calls["Show"] != 1 {
		t.Errorf("after first run: want 1 search call, got %d", mock.calls["Show"])
	}

	if _, err := run(context.Background(), p, taskCtx("interval-task")); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if mock.calls["Show"] != 1 {
		t.Errorf("after second run: want still 1 search call, got %d", mock.calls["Show"])
	}
}

// TestDiscoverServesCachedResultsWithinInterval covers the downstream-
// continuity guarantee: when a title is within its cooldown the search
// backend is not re-hit, but the previously-returned results are
// re-emitted so the rest of the pipeline still has entries to act on.
// Before this behaviour discover silently dropped the title until the
// TTL expired, which made repeated dry-runs produce empty pipelines.
func TestDiscoverServesCachedResultsWithinInterval(t *testing.T) {
	mock := newMockSearch("cache-serve")
	mock.results["Show"] = []*entry.Entry{
		entry.New("Show.S01E01", "http://example.com/1"),
		entry.New("Show.S01E02", "http://example.com/2"),
	}
	p := buildPlugin(t, mock, []string{"Show"}, nil)

	first, err := run(context.Background(), p, taskCtx("cache-serve"))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first run: got %d entries, want 2", len(first))
	}

	second, err := run(context.Background(), p, taskCtx("cache-serve"))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if mock.calls["Show"] != 1 {
		t.Errorf("backend should not be re-hit within interval, got %d calls", mock.calls["Show"])
	}
	if len(second) != 2 {
		t.Errorf("second run should serve 2 cached entries, got %d", len(second))
	}
	// URL round-trip is the load-bearing field for downstream sinks; verify
	// it survived JSON serialization.
	urls := map[string]bool{}
	for _, e := range second {
		urls[e.URL] = true
	}
	if !urls["http://example.com/1"] || !urls["http://example.com/2"] {
		t.Errorf("cached URLs not preserved: %v", urls)
	}
}

// TestDiscoverCachePreservesJackettStyleFields confirms that the typed
// fields jackett sets on search results (year, season, ints; seeds,
// info-hash, source strings) survive a JSON round-trip through the
// bucket. The Entry's Quality()/typed-struct accessors are documented as
// best-effort across JSON, but discover's backends populate the Fields
// map with primitives, which is the case we actually need to preserve.
func TestDiscoverCachePreservesJackettStyleFields(t *testing.T) {
	mock := newMockSearch("cache-fields")
	e := entry.New("Inception.2010.1080p", "http://example.com/inception")
	e.Set(entry.FieldVideoYear, 2010)
	e.Set(entry.FieldTorrentSeeds, 42)
	e.Set(entry.FieldTorrentInfoHash, "aabbcc")
	e.Set(entry.FieldSource, "jackett:idx")
	mock.results["Inception"] = []*entry.Entry{e}

	p := buildPlugin(t, mock, []string{"Inception"}, nil)
	if _, err := run(context.Background(), p, taskCtx("cache-fields")); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, err := run(context.Background(), p, taskCtx("cache-fields"))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 cached entry", len(got))
	}
	ce := got[0]
	if ce.GetInt(entry.FieldVideoYear) != 2010 {
		t.Errorf("video_year: got %d, want 2010 (int↔float64 round-trip)", ce.GetInt(entry.FieldVideoYear))
	}
	if ce.GetInt(entry.FieldTorrentSeeds) != 42 {
		t.Errorf("torrent_seeds: got %d, want 42", ce.GetInt(entry.FieldTorrentSeeds))
	}
	if ce.GetString(entry.FieldTorrentInfoHash) != "aabbcc" {
		t.Errorf("torrent_info_hash: got %q", ce.GetString(entry.FieldTorrentInfoHash))
	}
	if ce.GetString(entry.FieldSource) != "jackett:idx" {
		t.Errorf("source: got %q", ce.GetString(entry.FieldSource))
	}
	if ce.URL != "http://example.com/inception" || ce.Title != "Inception.2010.1080p" {
		t.Errorf("URL/Title not preserved: %q / %q", ce.URL, ce.Title)
	}
}

// TestDiscoverDryRunBypassesBucket covers the dry-run idempotency
// guarantee: a dry-run must fully exercise the search backends (read
// bypass) and must not write a "just-searched" record (write bypass),
// or subsequent real runs would silently no-op within the cooldown.
func TestDiscoverDryRunBypassesBucket(t *testing.T) {
	mock := newMockSearch("dry-bypass")
	mock.results["Show"] = []*entry.Entry{entry.New("Show.S01E01", "http://example.com/1")}
	p := buildPlugin(t, mock, []string{"Show"}, nil)

	dry := taskCtx("dry-bypass")
	dry.DryRun = true

	// Two dry-runs in a row: each one must re-hit the backend (no cache read).
	if _, err := run(context.Background(), p, dry); err != nil {
		t.Fatalf("first dry run: %v", err)
	}
	if _, err := run(context.Background(), p, dry); err != nil {
		t.Fatalf("second dry run: %v", err)
	}
	if mock.calls["Show"] != 2 {
		t.Errorf("dry-run should re-hit backend: got %d calls, want 2", mock.calls["Show"])
	}

	// A subsequent real run must also re-hit the backend (no cache write
	// happened during dry-runs).
	if _, err := run(context.Background(), p, taskCtx("dry-bypass")); err != nil {
		t.Fatalf("real run: %v", err)
	}
	if mock.calls["Show"] != 3 {
		t.Errorf("real run after dry-runs should still search: got %d calls, want 3", mock.calls["Show"])
	}
}

// TestDiscoverLegacyRecordIsTreatedAsCacheMiss covers the on-disk
// auto-migration: a record from before the results-caching feature has
// LastSearched set but no Results field, which decodes as Results==nil.
// Such a record must trigger a fresh search rather than emit zero
// entries until the TTL expires (otherwise upgrading would leave every
// previously-poisoned bucket producing empty discover output for up to
// a full cooldown window).
func TestDiscoverLegacyRecordIsTreatedAsCacheMiss(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mock := newMockSearch("legacy-heal")
	mock.results["Show"] = []*entry.Entry{entry.New("Show.S01E01", "http://example.com/1")}
	pluginName := registerMock(mock)
	p, err := newPlugin(map[string]any{
		"titles":   []any{"Show"},
		"search":   []any{pluginName},
		"interval": "1h",
	}, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	dp := p.(*discoverPlugin)

	// Seed a legacy record: timestamp only, no Results field.
	type legacyRecord struct {
		LastSearched time.Time `json:"last_searched"`
	}
	bucket := db.Bucket("discover:legacy-task")
	if err := bucket.Put("show", legacyRecord{LastSearched: time.Now().UTC()}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	got, err := run(context.Background(), dp, taskCtx("legacy-task"))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d entries, want 1 (legacy record should auto-heal)", len(got))
	}
	if mock.calls["Show"] != 1 {
		t.Errorf("backend should have been hit, got %d calls", mock.calls["Show"])
	}

	// After auto-heal, the record now has Results — the next run within
	// the interval serves from cache without hitting the backend.
	if _, err := run(context.Background(), dp, taskCtx("legacy-task")); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if mock.calls["Show"] != 1 {
		t.Errorf("second run should serve from healed cache, got %d total calls", mock.calls["Show"])
	}
}

// TestDiscoverCachesEmptyResultSet distinguishes a legitimate
// "search found nothing for this title" cache from a legacy record.
// A second run within the interval must NOT re-hit the backend, even
// though the cached results are empty.
func TestDiscoverCachesEmptyResultSet(t *testing.T) {
	mock := newMockSearch("empty-cache")
	// No mock.results entry → Search returns nil for "Phantom".
	p := buildPlugin(t, mock, []string{"Phantom"}, nil)

	if _, err := run(context.Background(), p, taskCtx("empty-cache")); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if mock.calls["Phantom"] != 1 {
		t.Fatalf("first run: want 1 call, got %d", mock.calls["Phantom"])
	}
	if _, err := run(context.Background(), p, taskCtx("empty-cache")); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if mock.calls["Phantom"] != 1 {
		t.Errorf("zero-result cache should suppress re-search, got %d calls", mock.calls["Phantom"])
	}
}

// TestDiscoverDryRunDoesNotPoisonRealRunInterval is the regression test
// for the user-reported bug: pre-fix, a dry-run stamped LastSearched=now
// for every title, and every subsequent real run within 24h silently
// produced zero entries because every title was "within interval".
func TestDiscoverDryRunDoesNotPoisonRealRunInterval(t *testing.T) {
	mock := newMockSearch("no-poison")
	mock.results["Show"] = []*entry.Entry{entry.New("Show.S01E01", "http://example.com/1")}
	p := buildPlugin(t, mock, []string{"Show"}, nil)

	dry := taskCtx("no-poison")
	dry.DryRun = true
	if _, err := run(context.Background(), p, dry); err != nil {
		t.Fatalf("dry run: %v", err)
	}

	got, err := run(context.Background(), p, taskCtx("no-poison"))
	if err != nil {
		t.Fatalf("real run: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("real run after dry-run got %d entries, want 1 (cache must not have been written)", len(got))
	}
}

func TestDiscoverMultipleTitles(t *testing.T) {
	mock := newMockSearch("multi")
	mock.results["Show A"] = []*entry.Entry{entry.New("A.S01E01", "http://example.com/a1")}
	mock.results["Show B"] = []*entry.Entry{entry.New("B.S01E01", "http://example.com/b1")}

	p := buildPlugin(t, mock, []string{"Show A", "Show B"}, nil)
	got, err := run(context.Background(), p, taskCtx("t"))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d entries, want 2", len(got))
	}
}

func TestDiscoverMissingSearch(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"titles": []any{"Show"},
	}, nil)
	if err == nil {
		t.Error("expected error when search is missing")
	}
}

func TestDiscoverInvalidInterval(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"titles":   []any{"Show"},
		"search":   []any{"x"},
		"interval": "not-a-duration",
	}, nil)
	if err == nil {
		t.Error("expected error for invalid interval")
	}
}

func TestDiscoverUnknownSearchPlugin(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"titles": []any{"Show"},
		"search": []any{"no-such-search-plugin"},
	}, nil)
	if err == nil {
		t.Error("expected error for unknown search plugin")
	}
}

// --- upstream entries supply titles ---

func TestDiscoverUpstreamSuppliesTitles(t *testing.T) {
	mock := newMockSearch("upstream")
	mock.results["Dynamic Show"] = []*entry.Entry{
		entry.New("Dynamic.Show.S01E01.720p", "http://example.com/1"),
	}

	p := buildPlugin(t, mock, nil, nil)
	got, err := p.Process(context.Background(), taskCtx("t"), []*entry.Entry{
		entry.New("Dynamic Show", ""),
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("want 1 entry from upstream-supplied title, got %d", len(got))
	}
}

func TestDiscoverDeduplicatesTitlesAcrossSources(t *testing.T) {
	mock := newMockSearch("upstream-dedup")
	mock.results["My Show"] = []*entry.Entry{
		entry.New("My.Show.S01E01", "http://example.com/1"),
	}

	// Same logical title appears as a static config entry AND as upstream
	// entries with case variants — search should run exactly once.
	p := buildPlugin(t, mock, []string{"My Show"}, nil)
	_, err := p.Process(context.Background(), taskCtx("dedup-task"), []*entry.Entry{
		entry.New("My Show", ""),
		entry.New("my show", ""),
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if mock.calls["My Show"] != 1 {
		t.Errorf("want exactly 1 search for deduplicated title, got %d", mock.calls["My Show"])
	}
}

func TestDiscoverForwardsHintFieldsFromUpstream(t *testing.T) {
	mock := newMockSearch("forward-hints")
	mock.results["Inception"] = []*entry.Entry{entry.New("Inception.2010.1080p", "http://example.com/1")}

	// Upstream entry carries the title plus hints that a trakt_list or
	// metainfo step would have provided.
	upstream := entry.New("Inception", "")
	upstream.Set(entry.FieldMediaType, entry.MediaTypeMovie)
	upstream.Set(entry.FieldVideoYear, 2010)
	upstream.Set(entry.FieldVideoImdbID, "tt1375666")

	p := buildPlugin(t, mock, nil, nil)
	_, err := p.Process(context.Background(), taskCtx("hints-task"), []*entry.Entry{upstream})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	qe := mock.queries["Inception"]
	if qe == nil {
		t.Fatal("expected mock to be called for Inception")
	}
	if got := qe.GetInt(entry.FieldVideoYear); got != 2010 {
		t.Errorf("video_year not forwarded: got %d, want 2010", got)
	}
	if got := qe.GetString(entry.FieldVideoImdbID); got != "tt1375666" {
		t.Errorf("video_imdb_id not forwarded: got %q, want tt1375666", got)
	}
	if got := qe.GetString(entry.FieldMediaType); got != entry.MediaTypeMovie {
		t.Errorf("media_type not forwarded: got %q, want movie", got)
	}
}

func TestDiscoverUpstreamEntryWinsOverStaticTitle(t *testing.T) {
	mock := newMockSearch("hint-priority")
	mock.results["Inception"] = []*entry.Entry{entry.New("Inception.2010", "http://example.com/1")}

	upstream := entry.New("Inception", "")
	upstream.Set(entry.FieldVideoYear, 2010)

	// Same title as the static config; upstream's hint fields should win.
	p := buildPlugin(t, mock, []string{"Inception"}, nil)
	_, err := p.Process(context.Background(), taskCtx("hint-priority-task"), []*entry.Entry{upstream})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := mock.queries["Inception"].GetInt(entry.FieldVideoYear); got != 2010 {
		t.Errorf("expected upstream hint to beat bare static title (year): got %d, want 2010", got)
	}
}
