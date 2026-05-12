// Package dag provides a directed acyclic graph model for pipeline topologies.
//
// A Graph is a set of Nodes where each Node references zero or more upstream
// Nodes by ID. Sources have no upstreams; sinks have no downstream consumers.
// Processors sit between them. The graph must be acyclic; Validate enforces this.
package dag

import (
	"fmt"
	"slices"
)

// NodeID is the unique identifier for a node within a graph.
type NodeID string

// Node is a single vertex in the pipeline graph.
type Node struct {
	// ID is unique within the graph.
	ID NodeID
	// PluginName is the registered plugin name (looked up in the plugin registry).
	PluginName string
	// Config is the plugin's configuration block.
	Config map[string]any
	// Upstreams lists the IDs of nodes whose output feeds into this node.
	// Empty for source nodes.
	Upstreams []NodeID
}

// Graph is a collection of Nodes forming a directed acyclic graph.
type Graph struct {
	// nodes is keyed by NodeID, insertion-ordered via order slice.
	nodes map[NodeID]*Node
	order []NodeID // insertion order, for deterministic iteration
}

// New returns an empty Graph.
func New() *Graph {
	return &Graph{nodes: make(map[NodeID]*Node)}
}

// AddNode adds a node to the graph. Returns an error if a node with the same
// ID already exists, or if any referenced upstream ID does not exist yet.
func (g *Graph) AddNode(n *Node) error {
	if _, exists := g.nodes[n.ID]; exists {
		return fmt.Errorf("dag: duplicate node ID %q", n.ID)
	}
	for _, up := range n.Upstreams {
		if _, ok := g.nodes[up]; !ok {
			return fmt.Errorf("dag: node %q references unknown upstream %q", n.ID, up)
		}
	}
	g.nodes[n.ID] = n
	g.order = append(g.order, n.ID)
	return nil
}

// Node returns the node with the given ID, or nil if not found.
func (g *Graph) Node(id NodeID) *Node {
	return g.nodes[id]
}

// Nodes returns all nodes in insertion order.
func (g *Graph) Nodes() []*Node {
	out := make([]*Node, 0, len(g.order))
	for _, id := range g.order {
		out = append(out, g.nodes[id])
	}
	return out
}

// Len returns the number of nodes.
func (g *Graph) Len() int { return len(g.nodes) }

// Downstreams returns all nodes whose Upstreams list contains id.
func (g *Graph) Downstreams(id NodeID) []*Node {
	var out []*Node
	for _, n := range g.nodes {
		if slices.Contains(n.Upstreams, id) {
			out = append(out, n)
		}
	}
	return out
}

// Sources returns nodes with no upstreams (entry points of the graph).
func (g *Graph) Sources() []*Node {
	var out []*Node
	for _, id := range g.order {
		n := g.nodes[id]
		if len(n.Upstreams) == 0 {
			out = append(out, n)
		}
	}
	return out
}

// Sinks returns nodes with no downstream consumers (terminal exit points of the
// graph). In a sink chain (sink → sink), only the last sink in the chain is
// returned. Intermediate sinks have downstream consumers and are not included.
func (g *Graph) Sinks() []*Node {
	var out []*Node
	for _, id := range g.order {
		n := g.nodes[id]
		if len(g.Downstreams(n.ID)) == 0 {
			out = append(out, n)
		}
	}
	return out
}
