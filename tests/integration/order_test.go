package integration

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/config"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName: "mock_input",
		Role:       plugin.RoleSource,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &mockInput{}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName: "mock_filter",
		Role:       plugin.RoleProcessor,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &mockFilter{}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName: "mock_output",
		Role:       plugin.RoleSink,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &mockOutput{called: &outputCalled}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName: "order1",
		Role:       plugin.RoleProcessor,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &orderPlugin{name: "order1", order: &orderList}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName: "order2",
		Role:       plugin.RoleProcessor,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &orderPlugin{name: "order2", order: &orderList}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName: "order3",
		Role:       plugin.RoleProcessor,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &orderPlugin{name: "order3", order: &orderList}, nil
		},
	})
}

var (
	outputCalled bool
	orderList    []string
)

type mockInput struct{}

func (p *mockInput) Name() string        { return "mock_input" }
func (p *mockInput) Phase() plugin.Phase { return plugin.PhaseInput }
func (p *mockInput) Generate(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	e := entry.New("test", "http://test")
	return []*entry.Entry{e}, nil
}

type mockFilter struct{}

func (p *mockFilter) Name() string        { return "mock_filter" }
func (p *mockFilter) Phase() plugin.Phase { return plugin.PhaseFilter }
func (p *mockFilter) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Accept()
	}
	return entries, nil
}

type mockOutput struct {
	called *bool
}

func (p *mockOutput) Name() string        { return "mock_output" }
func (p *mockOutput) Phase() plugin.Phase { return plugin.PhaseOutput }
func (p *mockOutput) Consume(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	if len(entry.FilterAccepted(entries)) > 0 {
		*p.called = true
	}
	return nil
}

type orderPlugin struct {
	name  string
	order *[]string
}

func (p *orderPlugin) Name() string        { return p.name }
func (p *orderPlugin) Phase() plugin.Phase { return plugin.PhaseModify }
func (p *orderPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	*p.order = append(*p.order, p.name)
	return entries, nil
}

func TestDryRun(t *testing.T) {
	outputCalled = false
	cfg, err := config.ParseBytes([]byte(`
src = input("mock_input")
flt = process("mock_filter", from_=src)
output("mock_output", from_=flt)
pipeline("dry-run-test")
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tasks, err := config.BuildTasks(cfg, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	tk := tasks[0]

	// Without dry-run — output should be called.
	_, _ = tk.Run(context.Background())
	if !outputCalled {
		t.Error("Consume() was not called")
	}

	// Reset and test with dry-run — output should be skipped.
	outputCalled = false
	tk.SetDryRun(true)
	_, _ = tk.Run(context.Background())
	if outputCalled {
		t.Error("Consume() was called in dry-run mode")
	}
}

func TestPluginOrder(t *testing.T) {
	const cfgStar = `
src = input("mock_input")
o1  = process("order1", from_=src)
o2  = process("order2", from_=o1)
o3  = process("order3", from_=o2)
output("print", from_=o3)
pipeline("order-test")
`
	for i := 0; i < 10; i++ {
		orderList = []string{}
		cfg, err := config.ParseBytes([]byte(cfgStar))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		tasks, err := config.BuildTasks(cfg, nil, nil)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		_, _ = tasks[0].Run(context.Background())

		if len(orderList) == 3 {
			if orderList[0] != "order1" || orderList[1] != "order2" || orderList[2] != "order3" {
				t.Errorf("Iteration %d: wrong order: %v", i, orderList)
				return
			}
		}
	}
}
