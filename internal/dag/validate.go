package dag

import (
	"fmt"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// Validate checks the graph for structural and semantic correctness:
//  1. All upstream references exist (enforced by AddNode, but re-checked here).
//  2. No cycles (via topological sort).
//  3. Source plugins (RoleSource) have no upstreams.
//  4. Sink plugins (RoleSink) may only have downstream sink nodes (sink chaining).
//  5. Processor/sink plugins have at least one upstream.
//  6. Every field in Descriptor.Requires is produced by at least one
//     transitive upstream node (field reachability check).
//
// Returns a slice of all errors found; returns nil when the graph is valid.
func Validate(g *Graph, reg func(name string) (*plugin.Descriptor, bool)) []error {
	var errs []error

	// Topological sort catches cycles and gives us a processing order.
	layers, err := g.Layers()
	if err != nil {
		return []error{err}
	}

	// Build a map of which fields are reachable at each node (accumulated from
	// all transitive upstreams), keyed by NodeID.
	reachable := make(map[NodeID]map[string]bool, g.Len())

	// Process layers in order so that when we reach a node, all its upstreams
	// have already been processed.
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
				for _, d := range g.Downstreams(n.ID) {
					dd, ok := reg(d.PluginName)
					if !ok {
						continue // already reported as unknown plugin
					}
					if dd.EffectiveRole() != plugin.RoleSink {
						errs = append(errs, fmt.Errorf("node %q (plugin %q, role sink): downstream node %q must also be a sink", n.ID, n.PluginName, d.ID))
					}
				}
			}

			// Accumulate fields reachable at this node from all upstreams.
			fields := make(map[string]bool)
			for _, upID := range n.Upstreams {
				for f := range reachable[upID] {
					fields[f] = true
				}
			}

			// Check that all required fields are reachable.
			for _, req := range d.Requires {
				if !fields[req] {
					errs = append(errs, fmt.Errorf("node %q (plugin %q): required field %q is not produced by any upstream node", n.ID, n.PluginName, req))
				}
			}

			// Add this node's produced fields to the reachable set, then store
			// the accumulated set for downstream nodes to inherit.
			for _, prod := range d.Produces {
				fields[prod] = true
			}
			reachable[n.ID] = fields
		}
	}

	return errs
}
