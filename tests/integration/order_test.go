package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/config"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"

	_ "github.com/brunoga/pipeliner/plugins/processor/filter/accept_all"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/condition"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/dedup"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/file"
	_ "github.com/brunoga/pipeliner/plugins/sink/print"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
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

func (p *mockInput) Name() string { return "mock_input" }
func (p *mockInput) Generate(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	e := entry.New("test", "http://test")
	return []*entry.Entry{e}, nil
}

type mockFilter struct{}

func (p *mockFilter) Name() string { return "mock_filter" }
func (p *mockFilter) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Accept()
	}
	return entries, nil
}

type mockOutput struct {
	called *bool
}

func (p *mockOutput) Name() string { return "mock_output" }
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

func (p *orderPlugin) Name() string { return p.name }
func (p *orderPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	*p.order = append(*p.order, p.name)
	return entries, nil
}

func TestDryRun(t *testing.T) {
	outputCalled = false
	cfg, err := config.ParseBytes([]byte(`
src = input("mock_input")
flt = process("mock_filter", upstream=src)
output("mock_output", upstream=flt)
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
o1  = process("order1", upstream=src)
o2  = process("order2", upstream=o1)
o3  = process("order3", upstream=o2)
output("print", upstream=o3)
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

// TestDedupMustFollowTheVetoingGates is the executable form of the ordering
// requirement the settle window documents.
//
// dedup collapses a wave to one release per item, and whatever it keeps is
// chosen on quality tags, because that is all it has. So a gate that can refuse
// a release — a bitrate floor, a language condition — has to run BEFORE dedup:
// placed after it, the alternatives have already been discarded by the time the
// refusal happens and the item is lost for that run.
//
// Both pipelines below accept first (as the movies and series filters do, which
// is what gives dedup something to rank), then differ only in whether the gate
// precedes dedup. Same feed, same gate, opposite outcomes.
func TestDedupMustFollowTheVetoingGates(t *testing.T) {
	feed := []rssItem{
		// Best tags, but the gate refuses it — stands in for a starved encode
		// that a bitrate floor would reject.
		{"Wave.Movie.2026.2160p.BluRay.x265", "http://example.com/wave-2160p"},
		{"Wave.Movie.2026.1080p.BluRay.x264", "http://example.com/wave-1080p"},
	}
	const gate = `gate = process("condition", upstream=%s, rules=[{"reject": 'video_resolution == "2160p"'}])`

	run := func(t *testing.T, body string) []*entry.Entry {
		t.Helper()
		srv := rssServer(t, feed)
		defer srv.Close()
		res := buildAndRun(t, fmt.Sprintf(`
src  = input("rss", url=%q)
meta = process("metainfo_file", upstream=src)
acc  = process("accept_all", upstream=meta)
`+body+`
pipeline("t")
`, srv.URL))
		return acceptedEntries(res.entries)
	}

	t.Run("gate after dedup loses the item", func(t *testing.T) {
		accepted := run(t, `
dd   = process("dedup", upstream=acc)
`+fmt.Sprintf(gate, "dd")+`
output("print", upstream=gate)
`)
		if len(accepted) != 0 {
			t.Errorf("expected nothing to survive: dedup crowns the 2160p and discards the 1080p, then the gate refuses the 2160p. got %v",
				entryTitles(accepted))
		}
	})

	t.Run("gate before dedup keeps the runner-up", func(t *testing.T) {
		accepted := run(t, fmt.Sprintf(gate, "acc")+`
dd   = process("dedup", upstream=gate)
output("print", upstream=dd)
`)
		if len(accepted) != 1 {
			t.Fatalf("expected the runner-up to survive, got %v", entryTitles(accepted))
		}
		if !strings.Contains(accepted[0].Title, "1080p") {
			t.Errorf("survivor is %q, want the 1080p runner-up", accepted[0].Title)
		}
	})
}
