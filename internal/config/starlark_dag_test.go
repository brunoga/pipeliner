package config

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"

	_ "github.com/brunoga/pipeliner/plugins/filter/accept_all"
	_ "github.com/brunoga/pipeliner/plugins/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/input/rss"
	_ "github.com/brunoga/pipeliner/plugins/output/print"
)

func parseDAGOK(t *testing.T, src string) *Config {
	t.Helper()
	c, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	return c
}

func parseDAGFail(t *testing.T, src string) {
	t.Helper()
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDAG_SimplePipeline(t *testing.T) {
	c := parseDAGOK(t, `
src = input("rss", url="https://example.com/rss")
seen = process("seen", from_=src)
output("print", from_=seen)
pipeline("simple", schedule="1h")
`)
	if len(c.Graphs) != 1 {
		t.Fatalf("want 1 graph, got %d", len(c.Graphs))
	}
	g := c.Graphs["simple"]
	if g == nil {
		t.Fatal("graph 'simple' not found")
	}
	if g.Len() != 3 {
		t.Errorf("want 3 nodes, got %d", g.Len())
	}
	if sched := c.GraphSchedules["simple"]; sched != "1h" {
		t.Errorf("want schedule '1h', got %q", sched)
	}
}

func TestDAG_Merge(t *testing.T) {
	c := parseDAGOK(t, `
src1 = input("rss", url="https://feed1.com/rss")
src2 = input("rss", url="https://feed2.com/rss")
seen = process("seen", from_=merge(src1, src2))
output("print", from_=seen)
pipeline("merged")
`)
	g := c.Graphs["merged"]
	if g == nil {
		t.Fatal("graph 'merged' not found")
	}
	// src1, src2, seen, print = 4 nodes
	if g.Len() != 4 {
		t.Errorf("want 4 nodes, got %d", g.Len())
	}
	// seen should have two upstreams
	var seenNode *dag.Node
	for _, n := range g.Nodes() {
		if n.PluginName == "seen" {
			seenNode = n
			break
		}
	}
	if seenNode == nil {
		t.Fatal("seen node not found")
	}
	if len(seenNode.Upstreams) != 2 {
		t.Errorf("seen: want 2 upstreams, got %d", len(seenNode.Upstreams))
	}
}

func TestDAG_FanOut(t *testing.T) {
	c := parseDAGOK(t, `
src = input("rss", url="https://example.com/rss")
seen = process("seen", from_=src)
output("print", from_=seen)
output("print", from_=seen)
pipeline("fanout")
`)
	g := c.Graphs["fanout"]
	if g == nil {
		t.Fatal("graph 'fanout' not found")
	}
	// src, seen, print_0, print_1 = 4 nodes
	if g.Len() != 4 {
		t.Errorf("want 4 nodes, got %d", g.Len())
	}
	sinks := g.Sinks()
	if len(sinks) != 2 {
		t.Errorf("want 2 sinks, got %d", len(sinks))
	}
}

func TestDAG_MultiplePipelines(t *testing.T) {
	c := parseDAGOK(t, `
# Pipeline 1
src1 = input("rss", url="https://a.com/rss")
output("print", from_=src1)
pipeline("p1", schedule="30m")

# Pipeline 2
src2 = input("rss", url="https://b.com/rss")
output("print", from_=src2)
pipeline("p2", schedule="2h")
`)
	if len(c.Graphs) != 2 {
		t.Fatalf("want 2 graphs, got %d", len(c.Graphs))
	}
	if c.GraphSchedules["p1"] != "30m" {
		t.Errorf("p1 schedule: want '30m', got %q", c.GraphSchedules["p1"])
	}
	if c.GraphSchedules["p2"] != "2h" {
		t.Errorf("p2 schedule: want '2h', got %q", c.GraphSchedules["p2"])
	}
}

func TestDAG_TwoPipelines(t *testing.T) {
	c := parseDAGOK(t, `
src1 = input("rss", url="https://example.com/rss1")
output("print", from_=src1)
pipeline("pipeline-a")

src2 = input("rss", url="https://example.com/rss2")
output("print", from_=src2)
pipeline("pipeline-b")
`)
	if len(c.Graphs) != 2 {
		t.Errorf("want 2 DAG graphs, got %d", len(c.Graphs))
	}
}

func TestDAG_EmptyPipelineError(t *testing.T) {
	parseDAGFail(t, `pipeline("empty")`)
}

func TestDAG_Validate_UnknownPlugin(t *testing.T) {
	c := parseDAGOK(t, `
src = input("rss", url="https://example.com/rss")
output("nonexistent_plugin", from_=src)
pipeline("bad")
`)
	errs := Validate(c)
	if len(errs) == 0 {
		t.Error("want validation errors for unknown plugin, got none")
	}
}

func TestDAG_BuildTasks(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	c := parseDAGOK(t, `
src = input("rss", url="https://example.com/rss")
output("print", from_=src)
pipeline("built", schedule="1h")
`)
	tasks, err := BuildTasks(c, db, nil)
	if err != nil {
		t.Fatalf("BuildTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("want 1 task, got %d", len(tasks))
	}
	if tasks[0].Name() != "built" {
		t.Errorf("want task name 'built', got %q", tasks[0].Name())
	}
}

func TestDAG_MergeRequiresAtLeastTwo(t *testing.T) {
	parseDAGFail(t, `
src = input("rss", url="https://example.com/rss")
bad = merge(src)
pipeline("x")
`)
}

func TestDAG_NodeHandleFromWrongType(t *testing.T) {
	parseDAGFail(t, `
process("seen", from_="not-a-handle")
pipeline("x")
`)
}

// TestDAG_Validate_FieldRequirements checks that Validate catches missing field requirements.
func TestDAG_Validate_FieldRequirements(t *testing.T) {
	// Register a test plugin that requires a field no upstream produces.
	const pluginName = "test_requires_missing_field"
	if _, ok := plugin.Lookup(pluginName); !ok {
		plugin.Register(&plugin.Descriptor{
			PluginName:  pluginName,
			Description: "test plugin that requires an unprovided field",
			PluginPhase: plugin.PhaseFilter,
			Requires:    []string{"some_nonexistent_field"},
			Factory: func(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
				return nil, nil // never constructed in this test
			},
		})
	}

	c := parseDAGOK(t, `
src = input("rss", url="https://example.com/rss")
proc = process("test_requires_missing_field", from_=src)
output("print", from_=proc)
pipeline("field-check")
`)
	errs := Validate(c)
	if len(errs) == 0 {
		t.Error("want validation error for missing required field, got none")
	}
}
