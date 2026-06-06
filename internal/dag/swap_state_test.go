package dag_test

import (
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// fakeReg builds a minimal plugin.Descriptor lookup from a map.
func fakeReg(descs map[string]*plugin.Descriptor) func(string) (*plugin.Descriptor, bool) {
	return func(name string) (*plugin.Descriptor, bool) {
		d, ok := descs[name]
		return d, ok
	}
}

func swapStateDesc() *plugin.Descriptor {
	return &plugin.Descriptor{
		PluginName:  "swap_state",
		Role:        plugin.RoleProcessor,
		InputStates: entry.StatesAll,
	}
}

func mustAdd(t *testing.T, g *dag.Graph, n *dag.Node) {
	t.Helper()
	if err := g.AddNode(n); err != nil {
		t.Fatalf("AddNode %q: %v", n.ID, err)
	}
}

func warnedAbout(warnings []error, field, node string) bool {
	for _, w := range warnings {
		msg := w.Error()
		if strings.Contains(msg, field) &&
			strings.Contains(msg, node) &&
			strings.Contains(msg, "may not be present") {
			return true
		}
	}
	return false
}

// TestSwapState_DefeatsRequireNarrowing: a require() filter promotes its
// listed fields on Accepted/Undecided, but a downstream swap_state that
// revives rejected entries should make those fields uncertain again. The
// validator must emit a "may not be present" warning at the consuming node.
func TestSwapState_DefeatsRequireNarrowing(t *testing.T) {
	descs := map[string]*plugin.Descriptor{
		"src": {PluginName: "src", Role: plugin.RoleSource,
			Produces: []string{"title"}, MayProduce: []string{"video_year"}},
		"require":    {PluginName: "require", Role: plugin.RoleProcessor},
		"swap_state": swapStateDesc(),
		"consumer": {PluginName: "consumer", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("video_year")},
	}

	g := dag.New()
	mustAdd(t, g, &dag.Node{ID: "n_src", PluginName: "src"})
	mustAdd(t, g, &dag.Node{ID: "n_req", PluginName: "require",
		Upstreams: []dag.NodeID{"n_src"},
		Config:    map[string]any{"fields": []any{"video_year"}}})
	mustAdd(t, g, &dag.Node{ID: "n_swap", PluginName: "swap_state",
		Upstreams: []dag.NodeID{"n_req"},
		Config:    map[string]any{"swap": []any{"rejected", "accepted"}}})
	mustAdd(t, g, &dag.Node{ID: "n_consumer", PluginName: "consumer",
		Upstreams: []dag.NodeID{"n_swap"}})

	_, warnings := dag.Validate(g, fakeReg(descs))
	if !warnedAbout(warnings, "video_year", "n_consumer") {
		t.Fatalf("expected video_year warning after swap_state, got: %v", warnings)
	}
}

// TestSwapState_DefeatsConditionRejectAbsenceNarrowing: a condition node
// with a reject-absence rule ("reject when video_year == 0") promotes
// video_year on the passing buckets. A downstream swap_state that revives
// rejected entries should defeat that promotion.
func TestSwapState_DefeatsConditionRejectAbsenceNarrowing(t *testing.T) {
	descs := map[string]*plugin.Descriptor{
		"src": {PluginName: "src", Role: plugin.RoleSource,
			Produces: []string{"title"}, MayProduce: []string{"video_year"}},
		"condition":  {PluginName: "condition", Role: plugin.RoleProcessor},
		"swap_state": swapStateDesc(),
		"consumer": {PluginName: "consumer", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("video_year")},
	}

	g := dag.New()
	mustAdd(t, g, &dag.Node{ID: "n_src", PluginName: "src"})
	mustAdd(t, g, &dag.Node{ID: "n_cond", PluginName: "condition",
		Upstreams: []dag.NodeID{"n_src"},
		Config:    map[string]any{"reject": "video_year == 0"}})
	mustAdd(t, g, &dag.Node{ID: "n_swap", PluginName: "swap_state",
		Upstreams: []dag.NodeID{"n_cond"},
		Config:    map[string]any{"swap": []any{"rejected", "accepted"}}})
	mustAdd(t, g, &dag.Node{ID: "n_consumer", PluginName: "consumer",
		Upstreams: []dag.NodeID{"n_swap"}})

	_, warnings := dag.Validate(g, fakeReg(descs))
	if !warnedAbout(warnings, "video_year", "n_consumer") {
		t.Fatalf("expected video_year warning after swap_state across condition reject-absence, got: %v", warnings)
	}
}

// TestSwapState_BenignPosition: swap_state immediately before a sink that
// has no Requires (the dedup-cleanup pattern) must not emit spurious
// warnings — the swap is doing its intended job and the sink doesn't care
// about specific fields.
func TestSwapState_BenignPosition(t *testing.T) {
	descs := map[string]*plugin.Descriptor{
		"src": {PluginName: "src", Role: plugin.RoleSource,
			Produces: []string{"title", "file_location"}},
		"classifier": {PluginName: "classifier", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("title"),
			Produces: []string{"media_type"}},
		"swap_state": swapStateDesc(),
		"sink": {PluginName: "sink", Role: plugin.RoleSink},
	}

	g := dag.New()
	mustAdd(t, g, &dag.Node{ID: "n_src", PluginName: "src"})
	mustAdd(t, g, &dag.Node{ID: "n_cls", PluginName: "classifier",
		Upstreams: []dag.NodeID{"n_src"}})
	mustAdd(t, g, &dag.Node{ID: "n_swap", PluginName: "swap_state",
		Upstreams: []dag.NodeID{"n_cls"},
		Config:    map[string]any{"swap": []any{"accepted", "rejected"}}})
	mustAdd(t, g, &dag.Node{ID: "n_sink", PluginName: "sink",
		Upstreams: []dag.NodeID{"n_swap"}})

	errs, warnings := dag.Validate(g, fakeReg(descs))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for sink with no Requires after benign swap_state, got: %v", warnings)
	}
}
