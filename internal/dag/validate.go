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
				// Merge (N>1 upstreams): union for reachable, intersection for certain.
				reach = make(map[string]bool)
				cert = nil
				for i, upID := range n.Upstreams {
					for f := range reachable[upID] {
						reach[f] = true
					}
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

func copySet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}
