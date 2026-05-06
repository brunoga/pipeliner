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

// mockInputPlugin is an InputPlugin that returns pre-configured entries.
type mockInputPlugin struct {
	pluginName string
	entries    []*entry.Entry
}

func (m *mockInputPlugin) Name() string        { return m.pluginName }
func (m *mockInputPlugin) Phase() plugin.Phase { return plugin.PhaseFrom }
func (m *mockInputPlugin) Run(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	return m.entries, nil
}

// registerMockInput registers a mock from-plugin factory so MakeFromPlugin can find it.
func registerMockInput(mock *mockInputPlugin) string {
	name := "mock-input-" + mock.pluginName
	func() {
		defer func() { recover() }()
		plugin.Register(&plugin.Descriptor{
			PluginName:  name,
			PluginPhase: plugin.PhaseFrom,
			Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
				return mock, nil
			},
		})
	}()
	return name
}

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
func (m *mockSearch) Phase() plugin.Phase { return plugin.PhaseFrom }
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
			PluginName:  name,
			PluginPhase: plugin.PhaseFrom,
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
		"via":      []any{pluginName},
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

// --- tests ---

func TestDiscoverCollectsResults(t *testing.T) {
	mock := newMockSearch("collect")
	mock.results["Breaking Bad"] = []*entry.Entry{
		entry.New("Breaking.Bad.S01E01.720p", "http://example.com/1"),
		entry.New("Breaking.Bad.S01E02.720p", "http://example.com/2"),
	}

	p := buildPlugin(t, mock, []string{"Breaking Bad"}, nil)
	got, err := p.Run(context.Background(), taskCtx("t"))
	if err != nil {
		t.Fatalf("Run: %v", err)
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
	got, err := p.Run(context.Background(), taskCtx("t"))
	if err != nil {
		t.Fatalf("Run: %v", err)
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

	// First run — searches and records timestamp.
	if _, err := p.Run(context.Background(), taskCtx("interval-task")); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if mock.calls["Show"] != 1 {
		t.Errorf("after first run: want 1 search call, got %d", mock.calls["Show"])
	}

	// Second run within 1h interval — should be skipped.
	if _, err := p.Run(context.Background(), taskCtx("interval-task")); err != nil {
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
	got, err := p.Run(context.Background(), taskCtx("t"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d entries, want 2", len(got))
	}
}

func TestDiscoverMissingVia(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"titles": []any{"Show"},
			}, nil)
	if err == nil {
		t.Error("expected error when via is missing")
	}
}

func TestDiscoverInvalidInterval(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"titles":   []any{"Show"},
		"via":      []any{"x"},
		"interval": "not-a-duration",
			}, nil)
	if err == nil {
		t.Error("expected error for invalid interval")
	}
}

func TestDiscoverUnknownSearchPlugin(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"titles": []any{"Show"},
		"via":    []any{"no-such-search-plugin"},
			}, nil)
	if err == nil {
		t.Error("expected error for unknown search plugin")
	}
}

// --- from ---

func TestDiscoverFromSuppliesTitles(t *testing.T) {
	inp := &mockInputPlugin{
		pluginName: "from-supplier",
		entries: []*entry.Entry{
			entry.New("Dynamic Show", ""),
		},
	}
	inpName := registerMockInput(inp)

	mock := newMockSearch("from-search")
	mock.results["Dynamic Show"] = []*entry.Entry{
		entry.New("Dynamic.Show.S01E01.720p", "http://example.com/1"),
	}
	searchName := registerMock(mock)

	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	cfg := map[string]any{
		"from":     []any{inpName},
		"via":      []any{searchName},
		"interval": "1h",
	}
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	got, err := p.(*discoverPlugin).Run(context.Background(), taskCtx("t"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("want 1 entry from dynamic title, got %d", len(got))
	}
}

func TestDiscoverFromDeduplicatesTitles(t *testing.T) {
	// Static titles and from both supply the same title — should search only once.
	inp := &mockInputPlugin{
		pluginName: "from-dedup",
		entries: []*entry.Entry{
			entry.New("My Show", ""),
			entry.New("my show", ""), // case variant
		},
	}
	inpName := registerMockInput(inp)

	mock := newMockSearch("from-dedup-search")
	mock.results["My Show"] = []*entry.Entry{
		entry.New("My.Show.S01E01", "http://example.com/1"),
	}
	searchName := registerMock(mock)

	db2, _ := store.OpenSQLite(":memory:")
	defer db2.Close()
	cfg := map[string]any{
		"titles":   []any{"My Show"},
		"from":     []any{inpName},
		"via":      []any{searchName},
		"interval": "1h",
	}
	p, err := newPlugin(cfg, db2)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p.(*discoverPlugin).Run(context.Background(), taskCtx("dedup-task")) //nolint:errcheck
	if mock.calls["My Show"] != 1 {
		t.Errorf("want exactly 1 search for deduplicated title, got %d", mock.calls["My Show"])
	}
}
