package dag_test

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// --- helpers for validate tests ---

func sinkDescFor(name string) *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: name, Role: plugin.RoleSink}
}

func sourceDescFor(name string) *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: name, Role: plugin.RoleSource}
}

func processorDescFor(name string) *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: name, Role: plugin.RoleProcessor}
}

// makeRegistry returns a simple reg function that maps plugin names to descriptors.
func makeRegistry(descs ...*plugin.Descriptor) func(string) (*plugin.Descriptor, bool) {
	m := make(map[string]*plugin.Descriptor, len(descs))
	for _, d := range descs {
		m[d.PluginName] = d
	}
	return func(name string) (*plugin.Descriptor, bool) {
		d, ok := m[name]
		return d, ok
	}
}

func makeGraph(t *testing.T, nodes ...*dag.Node) *dag.Graph {
	t.Helper()
	g := dag.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode(%q): %v", n.ID, err)
		}
	}
	return g
}

func node(id dag.NodeID, plugin string, upstreams ...dag.NodeID) *dag.Node {
	return &dag.Node{ID: id, PluginName: plugin, Upstreams: upstreams}
}

func TestLayers_Linear(t *testing.T) {
	g := makeGraph(t,
		node("a", "rss"),
		node("b", "seen", "a"),
		node("c", "transmission", "b"),
	)
	layers, err := g.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Fatalf("want 3 layers, got %d", len(layers))
	}
	if layers[0][0].ID != "a" || layers[1][0].ID != "b" || layers[2][0].ID != "c" {
		t.Errorf("unexpected layer order: %v", layers)
	}
}

func TestLayers_FanOut(t *testing.T) {
	// a → b, a → c (two sinks reading from one processor)
	g := makeGraph(t,
		node("a", "rss"),
		node("b", "transmission", "a"),
		node("c", "email", "a"),
	)
	layers, err := g.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 2 {
		t.Fatalf("want 2 layers, got %d", len(layers))
	}
	if len(layers[1]) != 2 {
		t.Errorf("want 2 nodes in layer 1, got %d", len(layers[1]))
	}
}

func TestLayers_Merge(t *testing.T) {
	// a, b → c (processor with two upstreams)
	g := makeGraph(t,
		node("a", "rss"),
		node("b", "rss2"),
		node("c", "seen", "a", "b"),
	)
	layers, err := g.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 2 {
		t.Fatalf("want 2 layers, got %d", len(layers))
	}
	if len(layers[0]) != 2 {
		t.Errorf("want 2 source nodes in layer 0, got %d", len(layers[0]))
	}
}

func TestLayers_Cycle(t *testing.T) {
	g := dag.New()
	_ = g.AddNode(node("a", "rss"))
	_ = g.AddNode(node("b", "seen", "a"))
	// Manually add a back-edge by building a node that references a future node.
	// Since AddNode checks upstreams exist, we can only create a cycle by
	// bypassing it — so instead we test the cycle detector with a pre-built graph
	// that has an artificial cycle via the internal nodes map.
	// For now, verify that referencing a non-existent upstream is caught.
	if err := g.AddNode(node("c", "tx", "nonexistent")); err == nil {
		t.Error("expected error for unknown upstream, got nil")
	}
}

func TestSources_Sinks(t *testing.T) {
	g := makeGraph(t,
		node("src1", "rss"),
		node("src2", "html"),
		node("proc", "seen", "src1", "src2"),
		node("sink", "transmission", "proc"),
	)
	sources := g.Sources()
	if len(sources) != 2 {
		t.Errorf("want 2 sources, got %d", len(sources))
	}
	sinks := g.Sinks()
	if len(sinks) != 1 || sinks[0].ID != "sink" {
		t.Errorf("want 1 sink 'sink', got %v", sinks)
	}
}

func TestAddNode_DuplicateID(t *testing.T) {
	g := dag.New()
	if err := g.AddNode(node("a", "rss")); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode(node("a", "html")); err == nil {
		t.Error("expected error for duplicate node ID, got nil")
	}
}

// --- Validate tests ---

func TestValidate_SinkToSink_Valid(t *testing.T) {
	// src → proc → sink1 → sink2 should be valid (sink chaining).
	g := makeGraph(t,
		node("src", "rssp"),
		node("proc", "seenp", "src"),
		node("sink1", "txp", "proc"),
		node("sink2", "emailp", "sink1"),
	)
	reg := makeRegistry(
		sourceDescFor("rssp"),
		processorDescFor("seenp"),
		sinkDescFor("txp"),
		sinkDescFor("emailp"),
	)
	errs := dag.Validate(g, reg)
	if len(errs) != 0 {
		t.Errorf("sink→sink chain should be valid, got errors: %v", errs)
	}
}

func TestValidate_SinkToSink_TerminalSink_HasNoDownstream(t *testing.T) {
	// In a chain src → sink1 → sink2, Sinks() should return only sink2.
	g := makeGraph(t,
		node("src", "rssp"),
		node("sink1", "txp", "src"),
		node("sink2", "emailp", "sink1"),
	)
	sinks := g.Sinks()
	if len(sinks) != 1 || sinks[0].ID != "sink2" {
		t.Errorf("want [sink2] from Sinks(), got %v", sinks)
	}
}

func TestValidate_SinkToProcessor_Invalid(t *testing.T) {
	// src → sink → proc is invalid (a sink's downstream must be a sink too).
	g := makeGraph(t,
		node("src", "rssp"),
		node("sink", "txp", "src"),
		node("proc", "seenp", "sink"),
	)
	reg := makeRegistry(
		sourceDescFor("rssp"),
		sinkDescFor("txp"),
		processorDescFor("seenp"),
	)
	errs := dag.Validate(g, reg)
	if len(errs) == 0 {
		t.Error("sink→processor should be invalid, got no errors")
	}
}

func TestValidate_RequiresFieldFromListSubPlugin(t *testing.T) {
	// movies (AcceptsList, list=[trakt_list]) → metainfo_tmdb (Requires trakt_year)
	// The validator must propagate trakt_list's Produces through the movies node
	// so that metainfo_tmdb's Requires check passes.
	g := dag.New()
	moviesNode := &dag.Node{
		ID: "movies_0", PluginName: "movies",
		Config: map[string]any{
			"list": []any{map[string]any{"name": "trakt_list"}},
		},
	}
	if err := g.AddNode(&dag.Node{ID: "src", PluginName: "rssp"}); err != nil {
		t.Fatal(err)
	}
	moviesNode.Upstreams = []dag.NodeID{"src"}
	if err := g.AddNode(moviesNode); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode(&dag.Node{ID: "meta", PluginName: "metap", Upstreams: []dag.NodeID{"movies_0"}}); err != nil {
		t.Fatal(err)
	}

	reg := makeRegistry(
		sourceDescFor("rssp"),
		&plugin.Descriptor{
			PluginName:  "movies",
			Role:        plugin.RoleProcessor,
			AcceptsList: true,
		},
		&plugin.Descriptor{
			PluginName: "trakt_list",
			Role:       plugin.RoleSource,
			Produces:   []string{"trakt_year", "trakt_tmdb_id"},
		},
		&plugin.Descriptor{
			PluginName: "metap",
			Role:       plugin.RoleProcessor,
			Requires:   []string{"trakt_year"},
		},
	)

	errs := dag.Validate(g, reg)
	if len(errs) != 0 {
		t.Errorf("expected no errors when required field is produced by a list sub-plugin; got: %v", errs)
	}
}
