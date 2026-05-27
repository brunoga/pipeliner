package dag

import (
	"sort"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// NodeFieldSets holds the field availability sets computed for one node.
// Certain fields are guaranteed on every entry that passes through the node;
// reachable fields may or may not be present depending on upstream conditions.
type NodeFieldSets struct {
	// Certain contains fields guaranteed on every entry entering this node
	// (i.e. in the Produces of every upstream path).
	Certain []string `json:"certain"`
	// Reachable contains fields that might be present (Produces or MayProduce
	// of any upstream path). Certain is a subset of Reachable.
	Reachable []string `json:"reachable"`
}

// ComputeNodeFields runs the field-propagation pass over the graph and returns
// the certain and reachable field sets for every node, keyed by node ID.
// It does not validate Requires or role constraints — use Validate for that.
func ComputeNodeFields(g *Graph, reg func(name string) (*plugin.Descriptor, bool)) map[NodeID]NodeFieldSets {
	layers, err := g.Layers()
	if err != nil {
		return nil
	}

	// postReach/postCert: fields that EXIT each node (i.e. after adding its Produces).
	// These feed into downstream nodes' input sets.
	postReach := make(map[NodeID]map[string]bool, g.Len())
	postCert := make(map[NodeID]map[string]bool, g.Len())

	// result stores the INPUT field sets for each node (what enters it from upstreams).
	result := make(map[NodeID]NodeFieldSets, g.Len())

	for _, layer := range layers {
		for _, n := range layer {
			// Compute what ENTERS this node from its upstreams.
			inReach, inCert := inheritSets(n, postReach, postCert, nil, nil)

			// Apply port masks and guarantees before recording input state.
			// Infer field contracts from the port's accept expression.
			acceptExpr, _ := n.Config["_port_accept_expr"].(string)
			ApplyPortAcceptNarrowing(acceptExpr, inReach, inCert)

			// Record the input field sets — these are what a condition on this
			// node can reference.
			result[n.ID] = NodeFieldSets{
				Certain:   sortedKeys(inCert),
				Reachable: sortedKeys(inReach),
			}

			// Now compute the OUTPUT field sets by adding this node's Produces.
			outReach := copySet(inReach)
			outCert := copySet(inCert)

			if d, ok := reg(n.PluginName); ok {
				for _, f := range d.Produces {
					outReach[f] = true
					outCert[f] = true
				}
				for _, f := range d.MayProduce {
					outReach[f] = true
				}
				// Propagate fields from list= and search= sub-plugins.
				for _, key := range []string{"list", "search"} {
					items, ok2 := n.Config[key].([]any)
					if !ok2 {
						continue
					}
					for _, item := range items {
						subName, _, err := plugin.ResolveNameAndConfig(item)
						if err != nil {
							continue
						}
						if subDesc, ok3 := reg(subName); ok3 {
							for _, f := range subDesc.Produces {
								outReach[f] = true
								outCert[f] = true
							}
							for _, f := range subDesc.MayProduce {
								outReach[f] = true
							}
						}
					}
				}
			}

			// Apply condition narrowing so ComputeNodeFields output matches Validate.
			if n.PluginName == "condition" {
				applyConditionNarrowingValidate(n.Config, outReach, outCert)
			}

			// Mirror Validate: require nodes promote their listed fields.
			if n.PluginName == "require" {
				applyRequireNarrowing(n.Config, outReach, outCert)
			}

			postReach[n.ID] = outReach
			postCert[n.ID] = outCert
		}
	}

	return result
}

// inheritSets computes the initial reach/cert sets for a node from its
// upstreams, using the same merge logic as Validate.
func inheritSets(n *Node, reachable, certain map[NodeID]map[string]bool,
	_ *Graph, _ func(string) (*plugin.Descriptor, bool)) (reach, cert map[string]bool) {

	switch len(n.Upstreams) {
	case 0:
		return make(map[string]bool), make(map[string]bool)
	case 1:
		upID := n.Upstreams[0]
		return copySet(reachable[upID]), copySet(certain[upID])
	default:
		reach = make(map[string]bool)
		for _, upID := range n.Upstreams {
			for f := range reachable[upID] {
				reach[f] = true
			}
		}
		// Certain: intersection across upstreams (same logic as Validate).
		var c map[string]bool
		for i, upID := range n.Upstreams {
			if i == 0 {
				c = copySet(certain[upID])
			} else {
				for f := range c {
					if !certain[upID][f] {
						delete(c, f)
					}
				}
			}
		}
		if c == nil {
			c = make(map[string]bool)
		}
		return reach, c
	}
}

// mapKeys returns the keys of a map[string]bool as an unsorted []string.
func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return []string{}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
