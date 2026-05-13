package config

import (
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"

	_ "github.com/brunoga/pipeliner/plugins/processor/filter/accept_all"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
	_ "github.com/brunoga/pipeliner/plugins/sink/print"
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
seen = process("seen", upstream=src)
output("print", upstream=seen)
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
seen = process("seen", upstream=merge(src1, src2))
output("print", upstream=seen)
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
seen = process("seen", upstream=src)
output("print", upstream=seen)
output("print", upstream=seen)
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
output("print", upstream=src1)
pipeline("p1", schedule="30m")

# Pipeline 2
src2 = input("rss", url="https://b.com/rss")
output("print", upstream=src2)
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
output("print", upstream=src1)
pipeline("pipeline-a")

src2 = input("rss", url="https://example.com/rss2")
output("print", upstream=src2)
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
output("nonexistent_plugin", upstream=src)
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
output("print", upstream=src)
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
process("seen", upstream="not-a-handle")
pipeline("x")
`)
}

// TestDAG_ChainedOutputs verifies that output() returns a nodeHandle usable as
// upstream= for another output() (sink chaining).
func TestDAG_ChainedOutputs(t *testing.T) {
	c := parseDAGOK(t, `
src = input("rss", url="https://example.com/rss")
seen = process("seen", upstream=src)
out1 = output("print", upstream=seen)
output("print", upstream=out1)
pipeline("chained")
`)
	g := c.Graphs["chained"]
	if g == nil {
		t.Fatal("graph 'chained' not found")
	}
	// src, seen, print_0 (out1), print_1 = 4 nodes
	if g.Len() != 4 {
		t.Errorf("want 4 nodes, got %d", g.Len())
	}
	// The graph should have only one terminal sink (the last output).
	sinks := g.Sinks()
	if len(sinks) != 1 {
		t.Errorf("want 1 terminal sink, got %d: %v", len(sinks), sinks)
	}
	// Validate should pass with no errors (sink→sink is allowed).
	errs := Validate(c)
	if len(errs) != 0 {
		t.Errorf("chained outputs should be valid, got errors: %v", errs)
	}
}

// TestDAG_Validate_FieldRequirements checks that Validate catches missing field requirements.
func TestDAG_Validate_FieldRequirements(t *testing.T) {
	// Register a test plugin that requires a field no upstream produces.
	const pluginName = "test_requires_missing_field"
	if _, ok := plugin.Lookup(pluginName); !ok {
		plugin.Register(&plugin.Descriptor{
			PluginName:  pluginName,
			Description: "test plugin that requires an unprovided field",
			Role: plugin.RoleProcessor,
			Requires:    []string{"some_nonexistent_field"},
			Factory: func(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
				return nil, nil // never constructed in this test
			},
		})
	}

	c := parseDAGOK(t, `
src = input("rss", url="https://example.com/rss")
proc = process("test_requires_missing_field", upstream=src)
output("print", upstream=proc)
pipeline("field-check")
`)
	errs := Validate(c)
	if len(errs) == 0 {
		t.Error("want validation error for missing required field, got none")
	}
}

// ── user function tests ───────────────────────────────────────────────────────

const userFuncConfig = `
# A reusable dedup+quality filter.
# pipeliner:param quality  Minimum quality spec
def quality_filter(upstream, quality="1080p"):
    s = process("seen",    upstream=upstream)
    p = process("print",   upstream=s)
    return p

src      = input("rss", url="https://example.com/rss")
filtered = quality_filter(upstream=src, quality="720p")
pipeline("tv")
`

func TestUserFunctionDiscovery(t *testing.T) {
	defs := scanUserFunctions(userFuncConfig)
	fd, ok := defs["quality_filter"]
	if !ok {
		t.Fatal("quality_filter not discovered")
	}
	if fd.Role != "processor" {
		t.Errorf("role: got %q, want processor", fd.Role)
	}
	if fd.Description == "" {
		t.Error("description should be non-empty")
	}
	if len(fd.Params) != 1 {
		t.Fatalf("params: got %d, want 1", len(fd.Params))
	}
	if fd.Params[0].Name != "quality" {
		t.Errorf("param name: got %q, want quality", fd.Params[0].Name)
	}
	if fd.Params[0].Hint == "" {
		t.Error("param hint should be non-empty")
	}
}

func TestUserFunctionRuntimeTracking(t *testing.T) {
	c := parseDAGOK(t, userFuncConfig)

	calls, ok := c.FunctionCalls["tv"]
	if !ok || len(calls) == 0 {
		t.Fatal("no function calls recorded for pipeline tv")
	}
	fcr := calls[0]
	if fcr.FuncName != "quality_filter" {
		t.Errorf("func name: got %q, want quality_filter", fcr.FuncName)
	}
	if len(fcr.InternalNodeIDs) != 2 {
		t.Errorf("internal nodes: got %d, want 2", len(fcr.InternalNodeIDs))
	}
	if fcr.ReturnNodeID == "" {
		t.Error("return node ID should be set")
	}
	// The return node should be the last internal node (print_1).
	last := fcr.InternalNodeIDs[len(fcr.InternalNodeIDs)-1]
	if fcr.ReturnNodeID != last {
		t.Errorf("return node: got %q, want %q", fcr.ReturnNodeID, last)
	}
	// Call key must be the Starlark variable name ("filtered"), not the
	// position-derived "quality_filter@line:col" which is invalid as an identifier.
	if fcr.CallKey == "" || fcr.CallKey != "filtered" {
		t.Errorf("call key: got %q, want the variable name %q", fcr.CallKey, "filtered")
	}
	if strings.Contains(fcr.CallKey, "@") {
		t.Errorf("call key %q contains '@' — not a valid Starlark identifier", fcr.CallKey)
	}
}

func TestUserFunctionNodesStillInGraph(t *testing.T) {
	// Internal nodes are still part of the DAG graph (the visual editor hides
	// them in collapsed mode, but the executor sees all of them).
	c := parseDAGOK(t, userFuncConfig)
	g, ok := c.Graphs["tv"]
	if !ok {
		t.Fatal("graph tv not found")
	}
	// 3 nodes total: rss_0 (outer), seen_1 and print_2 (inside quality_filter).
	if g.Len() != 3 {
		t.Errorf("graph len: got %d, want 3", g.Len())
	}
}

func TestUserFunctionNotDiscoveredWithoutPipelinerComment(t *testing.T) {
	src := `
def helper(x):
    return x

src = input("rss", url="https://example.com")
pipeline("p")
`
	defs := scanUserFunctions(src)
	if _, ok := defs["helper"]; ok {
		t.Error("helper should not be discovered without a # pipeliner: comment")
	}
}

// TestUserFunctionReturnNodeIDNonLinear verifies that ReturnNodeID is set to
// the node the function actually returns, even when that node is NOT the last
// node created inside the function body.
func TestUserFunctionReturnNodeIDNonLinear(t *testing.T) {
	// quality_filter creates 'accepted' first, then 'filtered', but returns
	// 'accepted'. The old heuristic (last created = return) would set
	// ReturnNodeID to 'filtered', which is wrong.
	src := `
# pipeliner:param quality Quality threshold
def quality_filter(upstream, quality="1080p"):
    accepted = process("accept_all", upstream=upstream)
    filtered  = process("seen",     upstream=upstream)
    return accepted

src    = input("rss", url="https://example.com/rss")
result = quality_filter(upstream=src)
pipeline("nonlinear")
`
	c := parseDAGOK(t, src)
	calls, ok := c.FunctionCalls["nonlinear"]
	if !ok || len(calls) == 0 {
		t.Fatal("no function calls recorded")
	}
	fcr := calls[0]
	if fcr.ReturnNodeID == "" {
		t.Fatal("ReturnNodeID is empty")
	}
	// ReturnNodeID must be the accept_all node (first created), not seen (last created).
	var acceptID string
	for _, nid := range fcr.InternalNodeIDs {
		if strings.Contains(nid, "accept_all") {
			acceptID = nid
			break
		}
	}
	if acceptID == "" {
		t.Fatalf("accept_all node not found in InternalNodeIDs: %v", fcr.InternalNodeIDs)
	}
	if fcr.ReturnNodeID != acceptID {
		t.Errorf("ReturnNodeID = %q, want %q (the returned node, not the last created)", fcr.ReturnNodeID, acceptID)
	}
	// CallKey must also be "result" (the variable name), not "quality_filter@...".
	if fcr.CallKey != "result" {
		t.Errorf("CallKey = %q, want %q", fcr.CallKey, "result")
	}
}
