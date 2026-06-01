package discover

import (
	"context"
	"log/slog"
	"maps"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// mockSearch is a SearchPlugin that returns pre-configured entries per query.
type mockSearch struct {
	pluginName string
	results    map[string][]*entry.Entry
	calls      map[string]int
}

func newMockSearch(name string) *mockSearch {
	return &mockSearch{
		pluginName: name,
		results:    map[string][]*entry.Entry{},
		calls:      map[string]int{},
	}
}

func (m *mockSearch) Name() string        { return m.pluginName }
func (m *mockSearch) Search(_ context.Context, _ *plugin.TaskContext, query string) ([]*entry.Entry, error) {
	m.calls[query]++
	return m.results[query], nil
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
