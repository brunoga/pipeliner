package dag

import (
	"fmt"
	"sort"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// orderingWarnings reports nodes that can refuse a specific release sitting
// downstream of a node that has already discarded that release's alternatives.
//
// A collapsing plugin (dedup) keeps one release per item and drops the rest,
// and it chooses on quality tags because that is all it has. Put a node that
// can refuse a release below it and the alternatives are already gone when the
// refusal lands: the item is lost for that run, and — since the same wave comes
// back next run and collapses to the same winner — for every run after it.
//
// The motivating case: a film whose best-tagged release turned out to be a RAR
// archive. `content` rejected it, and the dozen other releases of the same
// film, one of them explicitly tagged NORAR, had already been discarded.
//
// Only RefusalPerRelease counts. A plugin that refuses on grounds that hold for
// the whole item — `seen` ("already downloaded"), `limit` ("enough items this
// run") — would refuse the alternatives for the same reason, so its position
// relative to the collapse makes no difference.
//
// A refusal also only costs the item if it sits on *every* route from the
// collapse to a sink. A fan-out branch that exists for an optional side effect
// — adding the show to a favourites list, say — can refuse freely: the download
// branch beside it still delivers, so nothing is lost. The check therefore
// warns only when skipping the refusing node leaves no path to any sink.
func orderingWarnings(g *Graph, reg func(name string) (*plugin.Descriptor, bool)) []error {
	var out []error
	for _, n := range g.Nodes() {
		d, ok := reg(n.PluginName)
		if !ok || !d.Collapses {
			continue
		}
		type hit struct{ id, plug string }
		var hits []hit
		seen := map[NodeID]bool{n.ID: true}
		stack := append([]*Node(nil), g.Downstreams(n.ID)...)
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[cur.ID] {
				continue
			}
			seen[cur.ID] = true
			if dd, ok := reg(cur.PluginName); ok && dd.Refusal == plugin.RefusalPerRelease {
				if !reachesSinkAvoiding(g, reg, n.ID, cur.ID) {
					hits = append(hits, hit{string(cur.ID), cur.PluginName})
				}
			}
			stack = append(stack, g.Downstreams(cur.ID)...)
		}
		if len(hits) == 0 {
			continue
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].id < hits[j].id })
		for _, h := range hits {
			out = append(out, fmt.Errorf(
				"node %q (plugin %q) can refuse an individual release but runs downstream of node %q (plugin %q), "+
					"which keeps only one release per item: when it refuses, the alternatives are already gone and the "+
					"item is lost for this run and every run after it — move %q above %q",
				h.id, h.plug, n.ID, n.PluginName, h.id, n.ID))
		}
	}
	return out
}

// reachesSinkAvoiding reports whether any sink is reachable from the
// downstreams of from without passing through avoid. When it is, a refusal at
// avoid still leaves the item a way out of the pipeline, so the refusal costs
// that one branch rather than the item.
func reachesSinkAvoiding(g *Graph, reg func(string) (*plugin.Descriptor, bool), from, avoid NodeID) bool {
	seen := map[NodeID]bool{from: true, avoid: true}
	stack := append([]*Node(nil), g.Downstreams(from)...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur.ID] {
			continue
		}
		seen[cur.ID] = true
		if d, ok := reg(cur.PluginName); ok && d.EffectiveRole() == plugin.RoleSink {
			return true
		}
		stack = append(stack, g.Downstreams(cur.ID)...)
	}
	return false
}
