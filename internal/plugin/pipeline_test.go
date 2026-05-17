package plugin

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/store"
)

// --- mock plugins used in mini-pipeline tests ---

type mockSourcePlugin struct{ titles []string }

func (m *mockSourcePlugin) Name() string { return "mock_source" }
func (m *mockSourcePlugin) Generate(_ context.Context, _ *TaskContext) ([]*entry.Entry, error) {
	var out []*entry.Entry
	for i, t := range m.titles {
		out = append(out, entry.New(t, "http://example.com/"+string(rune('a'+i))))
	}
	return out, nil
}

type mockProcPlugin struct{ reject string }

func (m *mockProcPlugin) Name() string { return "mock_proc" }
func (m *mockProcPlugin) Process(_ context.Context, _ *TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.Title == m.reject {
			e.Reject("mock: rejected")
		} else {
			e.Accept()
		}
	}
	return entry.PassThrough(entries), nil
}

type mockSearchPlugin struct{}

func (m *mockSearchPlugin) Name() string { return "mock_search" }
func (m *mockSearchPlugin) Generate(_ context.Context, _ *TaskContext) ([]*entry.Entry, error) {
	return nil, nil
}
func (m *mockSearchPlugin) Search(_ context.Context, _ *TaskContext, query string) ([]*entry.Entry, error) {
	return []*entry.Entry{entry.New("result for "+query, "http://example.com/result")}, nil
}

// setupMocks resets the registry and registers the three mock plugins used by
// mini-pipeline tests. Must be called at the start of each test.
func setupMocks() {
	resetForTest()
	Register(&Descriptor{
		PluginName: "mock_source",
		Role:       RoleSource,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (Plugin, error) {
			var t []string
			for _, v := range cfg["titles"].([]any) {
				t = append(t, v.(string))
			}
			return &mockSourcePlugin{titles: t}, nil
		},
	})
	Register(&Descriptor{
		PluginName: "mock_proc",
		Role:       RoleProcessor,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (Plugin, error) {
			r, _ := cfg["reject"].(string)
			return &mockProcPlugin{reject: r}, nil
		},
	})
	Register(&Descriptor{
		PluginName:     "mock_search",
		Role:           RoleSource,
		IsSearchPlugin: true,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (Plugin, error) {
			return &mockSearchPlugin{}, nil
		},
	})
}

func pipelineTC() *TaskContext { return &TaskContext{Name: "test"} }

// --- tests ---

func TestMakeListPipelineSourceOnly(t *testing.T) {
	setupMocks()
	p := &NodePipeline{
		Steps: []NodePipelineStep{
			{PluginName: "mock_source", Config: map[string]any{"titles": []any{"Movie A", "Movie B"}}},
		},
	}
	src, err := MakeListPipeline(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := src.Generate(context.Background(), pipelineTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Title != "Movie A" || entries[1].Title != "Movie B" {
		t.Errorf("unexpected titles: %v, %v", entries[0].Title, entries[1].Title)
	}
}

func TestMakeListPipelineWithProcessor(t *testing.T) {
	setupMocks()
	p := &NodePipeline{
		Steps: []NodePipelineStep{
			{PluginName: "mock_source", Config: map[string]any{"titles": []any{"Keep", "Drop"}}},
			{PluginName: "mock_proc", Config: map[string]any{"reject": "Drop"}},
		},
	}
	src, err := MakeListPipeline(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := src.Generate(context.Background(), pipelineTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Title != "Keep" {
		t.Errorf("want [Keep], got %v", entries)
	}
}

func TestMakeListPipelineUnknownPlugin(t *testing.T) {
	resetForTest()
	p := &NodePipeline{
		Steps: []NodePipelineStep{
			{PluginName: "no_such_plugin", Config: map[string]any{}},
		},
	}
	_, err := MakeListPipeline(p, nil)
	if err == nil {
		t.Error("expected error for unknown plugin")
	}
}

func TestMakeListPipelineProcessorAsFirst(t *testing.T) {
	setupMocks()
	p := &NodePipeline{
		Steps: []NodePipelineStep{
			{PluginName: "mock_proc", Config: map[string]any{"reject": ""}},
		},
	}
	_, err := MakeListPipeline(p, nil)
	if err == nil {
		t.Error("expected error when first step is not a source")
	}
}

func TestMakeSearchPipeline(t *testing.T) {
	setupMocks()
	p := &NodePipeline{
		Steps: []NodePipelineStep{
			{PluginName: "mock_search", Config: map[string]any{}},
		},
	}
	sp, err := MakeSearchPipeline(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := sp.Search(context.Background(), pipelineTC(), "breaking bad")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Title != "result for breaking bad" {
		t.Errorf("unexpected search result: %v", entries)
	}
}

func TestMakeSearchPipelineWithProcessor(t *testing.T) {
	setupMocks()
	p := &NodePipeline{
		Steps: []NodePipelineStep{
			{PluginName: "mock_search", Config: map[string]any{}},
			{PluginName: "mock_proc", Config: map[string]any{"reject": "result for breaking bad"}},
		},
	}
	sp, err := MakeSearchPipeline(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := sp.Search(context.Background(), pipelineTC(), "breaking bad")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected processor to filter result, got %d entries", len(entries))
	}
}

func TestMakeSearchPipelineNonSearchFirst(t *testing.T) {
	setupMocks()
	p := &NodePipeline{
		Steps: []NodePipelineStep{
			{PluginName: "mock_source", Config: map[string]any{"titles": []any{}}},
		},
	}
	_, err := MakeSearchPipeline(p, nil)
	if err == nil {
		t.Error("expected error when first step is not a search plugin")
	}
}

func TestMiniPipelineSourceName(t *testing.T) {
	setupMocks()
	p := &NodePipeline{
		Steps: []NodePipelineStep{
			{PluginName: "mock_source", Config: map[string]any{"titles": []any{}}},
			{PluginName: "mock_proc", Config: map[string]any{"reject": ""}},
		},
	}
	src, err := MakeListPipeline(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if src.Name() != "mock_source→mock_proc" {
		t.Errorf("Name(): got %q", src.Name())
	}
}
