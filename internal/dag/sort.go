package dag

import (
	"fmt"
	"slices"
)

// Layers returns the nodes of the graph grouped into execution layers using
// Kahn's topological sort algorithm. All nodes in layer[i] can execute after
// all nodes in layer[i-1] have completed.
//
// Within each layer nodes are sorted alphabetically by ID for determinism.
// Returns an error if the graph contains a cycle.
func (g *Graph) Layers() ([][]*Node, error) {
	// inDegree[id] = number of upstreams not yet placed in a layer.
	inDegree := make(map[NodeID]int, len(g.nodes))
	for _, id := range g.order {
		n := g.nodes[id]
		inDegree[id] = len(n.Upstreams)
	}

	// Seed the queue with nodes that have no upstreams.
	var queue []NodeID
	for _, id := range g.order {
		if inDegree[id] == 0 {
			queue = append(queue, id)
		}
	}

	var layers [][]*Node
	placed := 0

	for len(queue) > 0 {
		// Sort current layer for determinism.
		slices.Sort(queue)

		layer := make([]*Node, len(queue))
		for i, id := range queue {
			layer[i] = g.nodes[id]
		}
		layers = append(layers, layer)
		placed += len(queue)

		// Decrement in-degree for each downstream of the current layer.
		var next []NodeID
		for _, id := range queue {
			for _, down := range g.Downstreams(id) {
				inDegree[down.ID]--
				if inDegree[down.ID] == 0 {
					next = append(next, down.ID)
				}
			}
		}
		queue = next
	}

	if placed != len(g.nodes) {
		return nil, fmt.Errorf("dag: graph contains a cycle")
	}
	return layers, nil
}
