package dag_test

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
)

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
