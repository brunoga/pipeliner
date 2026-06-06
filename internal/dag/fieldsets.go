package dag

import (
	"sort"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// NodeFieldSets holds the field availability sets computed for one node.
// Certain fields are guaranteed on every entry that the node's plugin sees;
// reachable fields may or may not be present depending on upstream conditions.
type NodeFieldSets struct {
	// Certain contains fields guaranteed on every entry that the node's
	// plugin sees — the intersection of the per-state certainty buckets
	// across the node's effective input states.
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

	// postReach/postCert: fields that EXIT each node (i.e. after adding its
	// Produces and any narrowing). These feed into downstream nodes' input
	// sets.
	postReach := make(map[NodeID]map[string]bool, g.Len())
	postCert := make(map[NodeID]stateCertainty, g.Len())

	// result stores the INPUT field sets for each node (what enters it from
	// upstreams), collapsed to a single set via the node's effective input
	// states so the visual editor's annotations match what the plugin will
	// actually see.
	result := make(map[NodeID]NodeFieldSets, g.Len())

	for _, layer := range layers {
		for _, n := range layer {
			// Compute what ENTERS this node from its upstreams.
			inReach, inCert := inheritReachAndCert(n, postReach, postCert, g, reg)

			// Apply port masks and guarantees before recording input state.
			acceptExpr, _ := n.Config["_port_accept_expr"].(string)
			applyPortAcceptNarrowingStateful(acceptExpr, inReach, &inCert)

			// Record the input field sets — these are what a condition on this
			// node can reference. Collapse per-state certainty to the
			// intersection across the plugin's effective input states. If
			// none of those states are populated (e.g. a sink in a pipeline
			// with no upstream Accept filter), fall back to the union of
			// populated buckets so the visual editor still shows the fields
			// an entry would carry if one ever reached this node.
			var effIn map[string]bool
			inStates := entry.StatesAcceptedUndecided
			if d, ok := reg(n.PluginName); ok {
				inStates = d.EffectiveInputStates()
			}
			effIn = inCert.effective(inStates)
			if len(effIn) == 0 && inCert.populated != 0 && inCert.populated&inStates == 0 {
				// Fall back to passing-state buckets (Accepted ∪ Undecided)
				// — matches Validate's fallback so the visual editor's
				// preview agrees with what server-side warnings will see.
				effIn = inCert.effective(inCert.populated & entry.StatesAcceptedUndecided)
			}
			result[n.ID] = NodeFieldSets{
				Certain:   sortedKeys(effIn),
				Reachable: sortedKeys(inReach),
			}

			// Now compute the OUTPUT field sets by adding this node's Produces.
			outReach := copySet(inReach)
			outCert := inCert.copy()

			if d, ok := reg(n.PluginName); ok {
				role := d.EffectiveRole()
				producingStates := producingStatesFor(d, role)
				if role == plugin.RoleSource {
					outCert.markPopulated(producingStates)
				} else if len(d.Produces) > 0 {
					if producingStates.Has(entry.Accepted) &&
						!outCert.populated.Has(entry.Accepted) &&
						outCert.populated.Has(entry.Undecided) {
						outCert.copyBucket(entry.Undecided, entry.Accepted)
					}
					outCert.markPopulated(producingStates)
				}
				for _, f := range d.Produces {
					outReach[f] = true
					outCert.addAll(producingStates, f)
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
								outCert.addAll(producingStates, f)
							}
							for _, f := range subDesc.MayProduce {
								outReach[f] = true
							}
						}
					}
				}
			}

			// Apply per-node narrowing/swap so output matches Validate.
			switch n.PluginName {
			case "condition":
				applyConditionNarrowingStateful(n.Config, outReach, &outCert)
			case "require":
				applyRequireNarrowingStateful(n.Config, outReach, &outCert)
			case "swap_state":
				applySwapStateNarrowing(n.Config, &outCert)
			}

			postReach[n.ID] = outReach
			postCert[n.ID] = outCert
		}
	}

	return result
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
