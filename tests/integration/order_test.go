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
		PluginName:  "mock_input",
		PluginPhase: plugin.PhaseInput,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &mockInput{}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName:  "mock_filter",
		PluginPhase: plugin.PhaseFilter,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &mockFilter{}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName:  "mock_output",
		PluginPhase: plugin.PhaseOutput,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &mockOutput{called: &outputCalled}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName:  "learn_input",
		PluginPhase: plugin.PhaseInput,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &learnInput{called: &learnCalled}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName:  "order1",
		PluginPhase: plugin.PhaseModify,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &orderPlugin{name: "order1", order: &orderList}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName:  "order2",
		PluginPhase: plugin.PhaseModify,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &orderPlugin{name: "order2", order: &orderList}, nil
		},
	})
	plugin.Register(&plugin.Descriptor{
		PluginName:  "order3",
		PluginPhase: plugin.PhaseModify,
		Factory: func(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &orderPlugin{name: "order3", order: &orderList}, nil
		},
	})
}

var (
	learnCalled  bool
	outputCalled bool
	orderList    []string
)

type mockInput struct{}

func (p *mockInput) Name() string        { return "mock_input" }
func (p *mockInput) Phase() plugin.Phase { return plugin.PhaseInput }
func (p *mockInput) Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return []*entry.Entry{{Title: "test", URL: "http://test"}}, nil
}

type mockFilter struct{}

func (p *mockFilter) Name() string        { return "mock_filter" }
func (p *mockFilter) Phase() plugin.Phase { return plugin.PhaseFilter }
func (p *mockFilter) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	e.Accept()
	return nil
}

type mockOutput struct {
	called *bool
}

func (p *mockOutput) Name() string        { return "mock_output" }
func (p *mockOutput) Phase() plugin.Phase { return plugin.PhaseOutput }
func (p *mockOutput) Output(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	*p.called = true
	return nil
}

type learnInput struct {
	called *bool
}

func (p *learnInput) Name() string        { return "learn_input" }
func (p *learnInput) Phase() plugin.Phase { return plugin.PhaseInput }
func (p *learnInput) Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return []*entry.Entry{{Title: "test", URL: "http://test"}}, nil
}
func (p *learnInput) Learn(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	*p.called = true
	return nil
}

type orderPlugin struct {
	name  string
	order *[]string
}

func (p *orderPlugin) Name() string        { return p.name }
func (p *orderPlugin) Phase() plugin.Phase { return plugin.PhaseModify }
func (p *orderPlugin) Modify(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	*p.order = append(*p.order, p.name)
	return nil
}

func TestLearnConsistency(t *testing.T) {
	learnCalled = false
	cfgYAML := `
tasks:
  learn-test:
    learn_input: {}
`
	cfg, _ := config.ParseBytes([]byte(cfgYAML))
	tasks, _ := config.BuildTasks(cfg, nil, nil)
	_, _ = tasks[0].Run(context.Background())

	if !learnCalled {
		t.Error("Learn() was not called on input plugin")
	}
}

func TestDryRun(t *testing.T) {
	outputCalled = false
	cfgYAML := `
tasks:
  dry-run-test:
    mock_input: {}
    mock_filter: {}
    mock_output: {}
`
	cfg, _ := config.ParseBytes([]byte(cfgYAML))
	tasks, _ := config.BuildTasks(cfg, nil, nil)
	tk := tasks[0]

	// Test without dry-run
	_, _ = tk.Run(context.Background())
	if !outputCalled {
		t.Error("Output() was not called")
	}

	// Reset and test with dry-run
	outputCalled = false
	tk.SetDryRun(true)
	_, _ = tk.Run(context.Background())
	if outputCalled {
		t.Error("Output() was called in dry-run mode")
	}
}

func TestPluginOrder(t *testing.T) {
	cfgYAML := `
tasks:
  order-test:
    mock_input: {}
    order1: {}
    order2: {}
    order3: {}
`

	for i := 0; i < 10; i++ {
		orderList = []string{}
		cfg, err := config.ParseBytes([]byte(cfgYAML))
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
