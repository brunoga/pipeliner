package dag

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/plugin"
)

func fakeReg(descs map[string]*plugin.Descriptor) func(string) (*plugin.Descriptor, bool) {
	return func(name string) (*plugin.Descriptor, bool) {
		d, ok := descs[name]
		return d, ok
	}
}

func TestComputeNodeFields_LinearPipeline(t *testing.T) {
	g := New()

	src := &Node{ID: "src", PluginName: "src_plugin"}
	proc := &Node{ID: "proc", PluginName: "proc_plugin", Upstreams: []NodeID{"src"}}
	sink := &Node{ID: "sink", PluginName: "sink_plugin", Upstreams: []NodeID{"proc"}}

	for _, n := range []*Node{src, proc, sink} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}

	reg := fakeReg(map[string]*plugin.Descriptor{
		"src_plugin":  {PluginName: "src_plugin", Role: plugin.RoleSource, Produces: []string{"source", "title"}, MayProduce: []string{"description"}},
		"proc_plugin": {PluginName: "proc_plugin", Role: plugin.RoleProcessor, Produces: []string{"enriched"}, MayProduce: []string{"video_year"}},
		"sink_plugin": {PluginName: "sink_plugin", Role: plugin.RoleSink},
	})

	fields := ComputeNodeFields(g, reg)

	// Source node starts empty.
	srcF := fields["src"]
	if len(srcF.Certain) != 0 || len(srcF.Reachable) != 0 {
		t.Errorf("source node should have empty input sets, got certain=%v reachable=%v", srcF.Certain, srcF.Reachable)
	}

	// Proc node sees src's output.
	procF := fields["proc"]
	if !containsStr(procF.Certain, "source") {
		t.Errorf("proc certain should contain source, got %v", procF.Certain)
	}
	if !containsStr(procF.Certain, "title") {
		t.Errorf("proc certain should contain title, got %v", procF.Certain)
	}
	if !containsStr(procF.Reachable, "description") {
		t.Errorf("proc reachable should contain description, got %v", procF.Reachable)
	}
	if containsStr(procF.Certain, "description") {
		t.Errorf("proc certain should NOT contain description (MayProduce only), got %v", procF.Certain)
	}

	// Sink node sees proc's output including enriched.
	sinkF := fields["sink"]
	if !containsStr(sinkF.Certain, "enriched") {
		t.Errorf("sink certain should contain enriched, got %v", sinkF.Certain)
	}
	if !containsStr(sinkF.Reachable, "video_year") {
		t.Errorf("sink reachable should contain video_year, got %v", sinkF.Reachable)
	}
}

func TestComputeNodeFields_MergeIntersection(t *testing.T) {
	g := New()

	a := &Node{ID: "a", PluginName: "a"}
	b := &Node{ID: "b", PluginName: "b"}
	merge := &Node{ID: "m", PluginName: "m", Upstreams: []NodeID{"a", "b"}}

	for _, n := range []*Node{a, b, merge} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}

	reg := fakeReg(map[string]*plugin.Descriptor{
		"a": {PluginName: "a", Role: plugin.RoleSource, Produces: []string{"x", "y"}},
		"b": {PluginName: "b", Role: plugin.RoleSource, Produces: []string{"x", "z"}},
		"m": {PluginName: "m", Role: plugin.RoleProcessor},
	})

	fields := ComputeNodeFields(g, reg)
	mf := fields["m"]

	// x is certain on both paths → certain after merge.
	if !containsStr(mf.Certain, "x") {
		t.Errorf("x should be certain after merge, got %v", mf.Certain)
	}
	// y is only certain on path a → NOT certain after non-route merge.
	if containsStr(mf.Certain, "y") {
		t.Errorf("y should NOT be certain after non-route merge, got %v", mf.Certain)
	}
	// Both y and z should still be reachable.
	if !containsStr(mf.Reachable, "y") || !containsStr(mf.Reachable, "z") {
		t.Errorf("y and z should be reachable after merge, got %v", mf.Reachable)
	}
}

func TestComputeNodeFields_EmptyGraph(t *testing.T) {
	g := New()
	fields := ComputeNodeFields(g, fakeReg(nil))
	if fields == nil {
		t.Error("expected empty map, got nil")
	}
	if len(fields) != 0 {
		t.Errorf("expected 0 entries, got %d", len(fields))
	}
}
