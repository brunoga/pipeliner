package dag

import (
	"fmt"
	"strings"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// Validate checks the graph for structural and semantic correctness:
//  1. All upstream references exist (enforced by AddNode, but re-checked here).
//  2. No cycles (via topological sort).
//  3. Source plugins (RoleSource) have no upstreams.
//  4. Sink plugins (RoleSink) may only have downstream sink nodes (sink chaining).
//  5. Processor/sink plugins have at least one upstream.
//  6. Every Requires group on a node has at least one field reachable from a
//     transitive upstream (error if none; warning if reachable but not certain).
//
// "Certain" fields are guaranteed on every entry: a node's Produces fields and
// the intersection of certain fields across all upstreams at a merge node.
// "Reachable" fields are potentially present: union of upstreams, plus MayProduce.
// A group satisfied only by reachable-but-not-certain fields emits a warning
// (merge gap or conditional MayProduce upstream).
//
// Returns (errors, warnings). Errors block pipeline load; warnings are advisory.
func Validate(g *Graph, reg func(name string) (*plugin.Descriptor, bool)) (errs, warnings []error) {
	// Topological sort catches cycles and provides processing order.
	layers, err := g.Layers()
	if err != nil {
		return []error{err}, nil
	}

	// Per-node field sets built up as we process layers in order.
	// reachable: union of all fields that might appear (Produces + MayProduce from any path).
	// certain:   intersection of Produces across all upstream paths (guaranteed on every entry).
	reachable := make(map[NodeID]map[string]bool, g.Len())
	certain := make(map[NodeID]map[string]bool, g.Len())

	for _, layer := range layers {
		for _, n := range layer {
			d, ok := reg(n.PluginName)
			if !ok {
				errs = append(errs, fmt.Errorf("node %q: unknown plugin %q", n.ID, n.PluginName))
				continue
			}
			role := d.EffectiveRole()

			// Structural role checks.
			if role == plugin.RoleSource && len(n.Upstreams) > 0 {
				errs = append(errs, fmt.Errorf("node %q (plugin %q, role source): source nodes must not have upstreams", n.ID, n.PluginName))
			}
			if role != plugin.RoleSource && len(n.Upstreams) == 0 {
				errs = append(errs, fmt.Errorf("node %q (plugin %q, role %s): non-source nodes must have at least one upstream", n.ID, n.PluginName, role))
			}
			if role == plugin.RoleSink {
				for _, dn := range g.Downstreams(n.ID) {
					dd, ok := reg(dn.PluginName)
					if !ok {
						continue // already reported as unknown plugin
					}
					if dd.EffectiveRole() != plugin.RoleSink {
						errs = append(errs, fmt.Errorf("node %q (plugin %q, role sink): downstream node %q must also be a sink", n.ID, n.PluginName, dn.ID))
					}
				}
			}

			// Build reachable and certain sets for this node from its upstreams.
			var reach, cert map[string]bool
			switch len(n.Upstreams) {
			case 0:
				// Source: starts with empty sets.
				reach = make(map[string]bool)
				cert = make(map[string]bool)
			case 1:
				// Single upstream: inherit both sets directly.
				upID := n.Upstreams[0]
				reach = copySet(reachable[upID])
				cert = copySet(certain[upID])
			default:
				// Merge (N>1 upstreams): union for reachable always.
				// For certain: use union when all upstreams belong to the same
				// route group (mutually exclusive ports), intersection otherwise.
				reach = make(map[string]bool)
				for _, upID := range n.Upstreams {
					for f := range reachable[upID] {
						reach[f] = true
					}
				}
				if routeGroup := sharedRouteGroup(n.Upstreams, g, reg); routeGroup != "" {
					// All upstreams are ports of the same route (mutually
					// exclusive branches — every entry arrives from exactly
					// one port). Use INTERSECTION so that fields masked on
					// any branch are not falsely considered certain after the
					// merge. Without masking all branch cert sets are
					// identical so intersection == union (no behaviour change).
					cert = nil
					for i, upID := range n.Upstreams {
						if i == 0 {
							cert = copySet(certain[upID])
						} else {
							for f := range cert {
								if !certain[upID][f] {
									delete(cert, f)
								}
							}
						}
					}
					if cert == nil {
						cert = make(map[string]bool)
					}
				} else {
					cert = nil
					for i, upID := range n.Upstreams {
						if i == 0 {
							cert = copySet(certain[upID])
						} else {
							for f := range cert {
								if !certain[upID][f] {
									delete(cert, f)
								}
							}
						}
					}
					if cert == nil {
						cert = make(map[string]bool)
					}
				}
			}

			// Infer field contracts from the port's accept expression.
			acceptExpr, _ := n.Config["_port_accept_expr"].(string)
			ApplyPortAcceptNarrowing(acceptExpr, reach, cert)

			// Check Requires groups against reachable/certain.
			for _, group := range d.Requires {
				reachFound, certFound := false, false
				for _, f := range group {
					if reach[f] {
						reachFound = true
					}
					if cert[f] {
						certFound = true
					}
				}
				groupStr := "[" + strings.Join(group, " | ") + "]"
				if !reachFound {
					errs = append(errs, fmt.Errorf("node %q (plugin %q): required field %s is not produced by any upstream node", n.ID, n.PluginName, groupStr))
				} else if !certFound {
					warnings = append(warnings, fmt.Errorf("node %q (plugin %q): required field %s may not be present on all entries (merge gap or conditional upstream)", n.ID, n.PluginName, groupStr))
				}
			}

			// Add this node's Produces to both sets, MayProduce to reachable only.
			for _, f := range d.Produces {
				reach[f] = true
				cert[f] = true
			}
			for _, f := range d.MayProduce {
				reach[f] = true
			}

			// For condition nodes: apply narrowing from the rule expressions so
			// that the Validate() field-certainty analysis matches what the UI
			// shows.  accept rules promote fields; reject rules either remove or
			// promote depending on whether they use presence or absence ops.
			if n.PluginName == "condition" {
				applyConditionNarrowingValidate(n.Config, reach, cert)
			}

			// Propagate fields from list= and search= sub-plugins configured on
			// this node. Their produced fields are visible to all downstream nodes.
			for _, key := range []string{"list", "search"} {
				items, ok := n.Config[key].([]any)
				if !ok {
					continue
				}
				for _, item := range items {
					subName, _, err := plugin.ResolveNameAndConfig(item)
					if err != nil {
						continue
					}
					if subDesc, ok2 := reg(subName); ok2 {
						for _, f := range subDesc.Produces {
							reach[f] = true
							cert[f] = true
						}
						for _, f := range subDesc.MayProduce {
							reach[f] = true
						}
					}
				}
			}

			reachable[n.ID] = reach
			certain[n.ID] = cert
		}
	}

	return errs, warnings
}

// sharedRouteGroup returns the route group ID if all upstreams (transitively
// through single-upstream chains) are route_selector nodes from the same
// group, indicating the branches are mutually exclusive. Returns "" otherwise.
func sharedRouteGroup(upstreams []NodeID, g *Graph, reg func(string) (*plugin.Descriptor, bool)) string {
	if len(upstreams) < 2 {
		return ""
	}
	var group string
	for _, upID := range upstreams {
		g2 := routeGroupOf(upID, g, reg)
		if g2 == "" {
			return ""
		}
		if group == "" {
			group = g2
		} else if group != g2 {
			return ""
		}
	}
	return group
}

// routeGroupOf returns the _route_group for nodeID if it is a route_selector
// or inherits one through a single-upstream chain.
func routeGroupOf(id NodeID, g *Graph, reg func(string) (*plugin.Descriptor, bool)) string {
	n := g.Node(id)
	if n == nil {
		return ""
	}
	// Direct route_selector.
	if n.PluginName == "route_selector" {
		if rg, _ := n.Config["_route_group"].(string); rg != "" {
			return rg
		}
	}
	// Propagate through single-upstream chains.
	if len(n.Upstreams) == 1 {
		return routeGroupOf(n.Upstreams[0], g, reg)
	}
	return ""
}

func copySet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// applyConditionNarrowingValidate applies condition-expression narrowing to the
// field sets being built by Validate().  It mirrors the client-side logic in
// visual-editor.js so that server-side field warnings are consistent with what
// the condition builder UI shows.
func applyConditionNarrowingValidate(config map[string]any, reach, cert map[string]bool) {
	type rule struct{ ruleType, expr string }
	var rules []rule

	// Single-rule format: {accept: "expr"} or {reject: "expr"}
	if accept, ok := config["accept"].(string); ok && accept != "" {
		rules = append(rules, rule{"accept", accept})
	}
	if reject, ok := config["reject"].(string); ok && reject != "" {
		rules = append(rules, rule{"reject", reject})
	}
	// Multi-rule format: {rules: [{accept: "..."}, ...]}
	if items, ok := config["rules"].([]any); ok {
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if accept, ok := m["accept"].(string); ok && accept != "" {
				rules = append(rules, rule{"accept", accept})
			}
			if reject, ok := m["reject"].(string); ok && reject != "" {
				rules = append(rules, rule{"reject", reject})
			}
		}
	}

	// Helper: convert map[string]bool to sorted []string.
	keys := func(m map[string]bool) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out
	}

	for _, r := range rules {
		certSlice  := keys(cert)
		reachSlice := keys(reach)

		switch r.ruleType {
		case "accept":
			// Accept rules promote fields to certain.
			for _, f := range NarrowCertain(r.expr, certSlice, reachSlice) {
				reach[f] = true
				cert[f] = true
			}
		case "reject":
			// Reject presence-op rules remove fields from reachable/certain.
			for _, f := range RejectPresenceRemoved(r.expr, reachSlice) {
				delete(reach, f)
				delete(cert, f)
			}
			// Reject absence-op rules promote fields to certain.
			for _, f := range RejectAbsencePromoted(r.expr, certSlice, reachSlice) {
				reach[f] = true
				cert[f] = true
			}
		}
	}
}

