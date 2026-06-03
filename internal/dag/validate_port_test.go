package dag_test

// Tests for port-level field inference from accept expressions.

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// buildPortGraph constructs a minimal route-with-ports graph:
//
//	source → route → selector_a (accept_expr exprA)
//	                → selector_b (accept_expr exprB)
//	                                     ↓ (optional: a node after selector_a)
func buildPortGraph(
	t *testing.T,
	exprA, exprB string,
	extraAfterA ...dag.NodeID,
) (*dag.Graph, func(string) (*plugin.Descriptor, bool)) {
	t.Helper()

	g := dag.New()

	addNode := func(id, plugin string, ups []dag.NodeID, cfg map[string]any) {
		t.Helper()
		if err := g.AddNode(&dag.Node{ID: dag.NodeID(id), PluginName: plugin, Upstreams: ups, Config: cfg}); err != nil {
			t.Fatalf("AddNode %q: %v", id, err)
		}
	}

	addNode("src", "source", nil, nil)
	addNode("route", "route", []dag.NodeID{"src"}, nil)

	selACfg := map[string]any{
		"_route_port_name": "a",
		"_route_group":     "rg1",
	}
	if exprA != "" {
		selACfg["_port_accept_expr"] = exprA
	}
	addNode("sel_a", "route_selector", []dag.NodeID{"route"}, selACfg)

	selBCfg := map[string]any{
		"_route_port_name": "b",
		"_route_group":     "rg1",
	}
	if exprB != "" {
		selBCfg["_port_accept_expr"] = exprB
	}
	addNode("sel_b", "route_selector", []dag.NodeID{"route"}, selBCfg)

	for _, extra := range extraAfterA {
		addNode(string(extra), "consumer", []dag.NodeID{"sel_a"}, nil)
	}

	reg := makeRegistry(
		&plugin.Descriptor{PluginName: "source", Role: plugin.RoleSource,
			MayProduce: []string{"torrent_url", "magnet_url"},
			Produces:   []string{"common"},
		},
		&plugin.Descriptor{PluginName: "route", Role: plugin.RoleProcessor},
		&plugin.Descriptor{PluginName: "route_selector", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("_route_port"),
		},
		processorDescFor("consumer"),
		sinkDescFor("sink"),
	)
	return g, reg
}

// TestPortAcceptExprPromotesMayProduce verifies that a presence-check accept
// expression (field != "") promotes a MayProduce field to certain on the branch.
// Downstream Requires passes without a warning.
func TestPortAcceptExprPromotesMayProduce(t *testing.T) {
	g, _ := buildPortGraph(t,
		`torrent_url != ""`, // branch a: presence check promotes torrent_url
		`magnet_url != ""`,  // branch b: presence check promotes magnet_url
	)
	// Add a consumer on branch a that Requires torrent_url.
	if err := g.AddNode(&dag.Node{
		ID: "consumer_a", PluginName: "consumer_req",
		Upstreams: []dag.NodeID{"sel_a"},
	}); err != nil {
		t.Fatal(err)
	}

	reg2 := makeRegistry(
		&plugin.Descriptor{PluginName: "source", Role: plugin.RoleSource,
			MayProduce: []string{"torrent_url", "magnet_url"},
			Produces:   []string{"common", "_route_port"},
		},
		&plugin.Descriptor{PluginName: "route", Role: plugin.RoleProcessor,
			Produces: []string{"_route_port"}},
		&plugin.Descriptor{PluginName: "route_selector", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("_route_port"),
		},
		&plugin.Descriptor{PluginName: "consumer_req", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("torrent_url"),
		},
	)
	errs, warnings := dag.Validate(g, reg2)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings (accept expr should promote MayProduce to certain), got: %v", warnings)
	}
}

// TestPortAbsenceExprCausesHardError verifies that an absence-check accept
// expression (field == "") removes the field from downstream reachable set,
// making a downstream Requires for that field a hard validation error.
func TestPortAbsenceExprCausesHardError(t *testing.T) {
	g := dag.New()
	addNode := func(id, plug string, ups []dag.NodeID, cfg map[string]any) {
		if err := g.AddNode(&dag.Node{ID: dag.NodeID(id), PluginName: plug, Upstreams: ups, Config: cfg}); err != nil {
			t.Fatalf("AddNode %q: %v", id, err)
		}
	}

	addNode("src", "source", nil, nil)
	addNode("route", "route", []dag.NodeID{"src"}, nil)
	// torrent branch: absence check on magnet_url removes it from reachable
	addNode("sel_t", "route_selector", []dag.NodeID{"route"}, map[string]any{
		"_route_port_name":  "torrent",
		"_route_group":      "rg1",
		"_port_accept_expr": `magnet_url == ""`,
	})
	// A consumer on the torrent branch mistakenly requires magnet_url.
	addNode("bad", "bad_consumer", []dag.NodeID{"sel_t"}, nil)

	reg := makeRegistry(
		&plugin.Descriptor{PluginName: "source", Role: plugin.RoleSource,
			Produces: []string{"torrent_url", "magnet_url", "_route_port"},
		},
		&plugin.Descriptor{PluginName: "route", Role: plugin.RoleProcessor},
		&plugin.Descriptor{PluginName: "route_selector", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("_route_port"),
		},
		&plugin.Descriptor{PluginName: "bad_consumer", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("magnet_url"), // absent on this branch
		},
	)
	errs, _ := dag.Validate(g, reg)
	if len(errs) == 0 {
		t.Error("expected a validation error for Requires on a field absent from accept expr, got none")
	}
}

// TestMergeIntersectionWithAcceptExprs verifies that after merging two route
// branches whose accept expressions infer different field sets, branch-specific
// fields are NOT certain at the merge point (warning instead of silent false-certain).
func TestMergeIntersectionWithAcceptExprs(t *testing.T) {
	g := dag.New()
	addNode := func(id, plug string, ups []dag.NodeID, cfg map[string]any) {
		if err := g.AddNode(&dag.Node{ID: dag.NodeID(id), PluginName: plug, Upstreams: ups, Config: cfg}); err != nil {
			t.Fatalf("AddNode %q: %v", id, err)
		}
	}

	addNode("src", "source", nil, nil)
	addNode("route", "route", []dag.NodeID{"src"}, nil)
	// torrent branch: promotes torrent_url, removes magnet_url
	addNode("sel_t", "route_selector", []dag.NodeID{"route"}, map[string]any{
		"_route_port_name": "torrent", "_route_group": "rg1",
		"_port_accept_expr": `torrent_url != "" and magnet_url == ""`,
	})
	// magnet branch: promotes magnet_url, removes torrent_url
	addNode("sel_m", "route_selector", []dag.NodeID{"route"}, map[string]any{
		"_route_port_name": "magnet", "_route_group": "rg1",
		"_port_accept_expr": `magnet_url != "" and torrent_url == ""`,
	})
	// Merge both branches back together.
	addNode("merge_node", "after_merge", []dag.NodeID{"sel_t", "sel_m"}, nil)

	reg := makeRegistry(
		&plugin.Descriptor{PluginName: "source", Role: plugin.RoleSource,
			MayProduce: []string{"torrent_url", "magnet_url"},
			Produces:   []string{"common", "_route_port"},
		},
		&plugin.Descriptor{PluginName: "route", Role: plugin.RoleProcessor},
		&plugin.Descriptor{PluginName: "route_selector", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("_route_port"),
		},
		// After merge: requires torrent_url — should warn, not error
		&plugin.Descriptor{PluginName: "after_merge", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("torrent_url"),
		},
	)
	errs, warnings := dag.Validate(g, reg)
	if len(errs) != 0 {
		t.Errorf("expected no hard errors after merge, got: %v", errs)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning for Requires on a branch-only field after merge, got none")
	}
}

// TestMergeNoChangeWithoutAcceptExprs verifies backward compatibility: without
// accept exprs both branches inherit identical field sets, so intersection ==
// union and common fields remain certain after merge.
func TestMergeNoChangeWithoutAcceptExprs(t *testing.T) {
	g := dag.New()
	addNode := func(id, plug string, ups []dag.NodeID, cfg map[string]any) {
		if err := g.AddNode(&dag.Node{ID: dag.NodeID(id), PluginName: plug, Upstreams: ups, Config: cfg}); err != nil {
			t.Fatalf("AddNode %q: %v", id, err)
		}
	}

	addNode("src", "source", nil, nil)
	addNode("route", "route", []dag.NodeID{"src"}, nil)
	addNode("sel_a", "route_selector", []dag.NodeID{"route"}, map[string]any{
		"_route_port_name": "a", "_route_group": "rg1",
	})
	addNode("sel_b", "route_selector", []dag.NodeID{"route"}, map[string]any{
		"_route_port_name": "b", "_route_group": "rg1",
	})
	addNode("merge_node", "after_merge", []dag.NodeID{"sel_a", "sel_b"}, nil)

	reg := makeRegistry(
		&plugin.Descriptor{PluginName: "source", Role: plugin.RoleSource,
			Produces: []string{"common", "_route_port"},
		},
		&plugin.Descriptor{PluginName: "route", Role: plugin.RoleProcessor},
		&plugin.Descriptor{PluginName: "route_selector", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("_route_port"),
		},
		// common is produced by source → should remain certain after merge
		&plugin.Descriptor{PluginName: "after_merge", Role: plugin.RoleProcessor,
			Requires: plugin.RequireAll("common"),
		},
	)
	errs, warnings := dag.Validate(g, reg)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for common field after maskless merge, got: %v", warnings)
	}
}
