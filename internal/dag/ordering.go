package dag

import (
	"fmt"
	"sort"

	"github.com/brunoga/pipeliner/internal/expr"
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
// The question is asked of the configured node, not of the plugin: `probe`
// can reject a release, but only with require=true, and a probe node without
// it leaves an unreadable release accepted. Warning about such a node names a
// refusal that cannot happen, which is how a reader learns to skip warnings.
//
// A refusal is also not lost when a later run will make it again BEFORE the
// collapse. The evidence has to be durable and replayed above the collapse for
// that — a `probe cache_only=true` node, which stamps what earlier runs
// learned for free — and then the refused release is refused again while its
// alternatives are still on the table, so the collapse picks the next one.
// See refusalIsReplayable.
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
			if dd, ok := reg(cur.PluginName); ok && dd.RefusalIn(cur.Config) == plugin.RefusalPerRelease {
				if !reachesSinkAvoiding(g, reg, n.ID, cur.ID) &&
					!refusalIsReplayable(g, reg, n.ID, cur.ID) {
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

// decisionFields lists the entry fields a refusing node's decision reads, or
// nil when there is no static answer. nil means "unknown", and an unknown
// decision is never excused below a collapse.
//
// Only the plugins whose decision is written out in the config can answer:
// `condition`'s rules, `require`'s field list, a route port's accept
// expression. `content` decides from a file list and `torrent_alive` from a
// scrape — nothing in their config says which release passes, so there is
// nothing a node above the collapse could have replayed.
func decisionFields(n *Node) []string {
	var out []string
	add := func(f string) {
		for _, s := range out {
			if s == f {
				return
			}
		}
		out = append(out, f)
	}
	if acceptExpr, _ := n.Config["_port_accept_expr"].(string); acceptExpr != "" {
		e, err := expr.Compile(acceptExpr)
		if err != nil {
			return nil
		}
		for _, f := range e.FieldRefs() {
			add(f)
		}
	}
	switch n.PluginName {
	case "condition":
		exprs := conditionRuleExprs(n.Config)
		if len(exprs) == 0 {
			return nil
		}
		for _, ex := range exprs {
			e, err := expr.Compile(ex)
			if err != nil {
				// An expression that will not compile is a different error,
				// reported elsewhere; here it means the fields are unknown.
				return nil
			}
			for _, f := range e.FieldRefs() {
				add(f)
			}
		}
	case "require":
		fields := requireFields(n.Config)
		if len(fields) == 0 {
			return nil
		}
		for _, f := range fields {
			add(f)
		}
	default:
		if len(out) == 0 {
			return nil
		}
	}
	return out
}

// refusalIsReplayable reports whether the refusal at ref, below collapse at
// col, is one a later run will make again before the collapse — in which case
// the alternatives are not lost, they are tried next time.
//
// The test is evidence, not intent: every field ref's decision reads must be
// produced, above the collapse, by a node that replays a durable per-release
// verdict, and those nodes must sit on every path from a source into the
// collapse. A `probe cache_only=true` node above `dedup` is the case this
// exists for — it replays what earlier runs learned for free, so a release
// refused on its own merits is refused again next run while its alternatives
// are still on the table, and the collapse picks the next one.
//
// It deliberately does not check that the two refusals agree. Nothing static
// could: the upper gate and the lower one are different rules by design (the
// lower one judges a fresh verdict, the upper one replays an old). What is
// checkable, and what the warning is actually about, is whether the evidence
// is available early enough to act on.
func refusalIsReplayable(g *Graph, reg func(string) (*plugin.Descriptor, bool), col, ref NodeID) bool {
	refNode := g.Node(ref)
	if refNode == nil {
		return false
	}
	fields := decisionFields(refNode)
	if len(fields) == 0 {
		return false
	}
	// Gates per field: which replaying ancestors of the collapse produce it.
	gates := make([]map[NodeID]bool, len(fields))
	for i := range gates {
		gates[i] = map[NodeID]bool{}
	}
	for _, a := range ancestors(g, col) {
		d, ok := reg(a.PluginName)
		if !ok || !d.ReplaysPerReleaseVerdict {
			continue
		}
		for i, f := range fields {
			if produces(d, f) {
				gates[i][a.ID] = true
			}
		}
	}
	for _, g2 := range gates {
		if len(g2) == 0 {
			return false
		}
		// A gate that only covers one branch of a merge leaves the other
		// branch's releases exactly as lost as before.
		if reachesSourceAvoiding(g, col, g2) {
			return false
		}
	}
	return true
}

// produces reports whether d writes field f, certainly or on some entries.
// MayProduce counts: a verdict is recorded for the releases that were probed,
// which is precisely the population the gate is there to refuse.
func produces(d *plugin.Descriptor, f string) bool {
	for _, p := range d.Produces {
		if p == f {
			return true
		}
	}
	for _, p := range d.MayProduce {
		if p == f {
			return true
		}
	}
	return false
}

// ancestors returns every node upstream of id, transitively.
func ancestors(g *Graph, id NodeID) []*Node {
	seen := map[NodeID]bool{id: true}
	var out []*Node
	n := g.Node(id)
	if n == nil {
		return nil
	}
	stack := append([]NodeID(nil), n.Upstreams...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		cn := g.Node(cur)
		if cn == nil {
			continue
		}
		out = append(out, cn)
		stack = append(stack, cn.Upstreams...)
	}
	return out
}

// reachesSourceAvoiding reports whether a source feeds from without passing
// through any node in gates. When it does, releases arriving that way reach
// the collapse with no replayed verdict on them.
func reachesSourceAvoiding(g *Graph, from NodeID, gates map[NodeID]bool) bool {
	n := g.Node(from)
	if n == nil {
		return false
	}
	if len(n.Upstreams) == 0 {
		return true
	}
	seen := map[NodeID]bool{from: true}
	stack := append([]NodeID(nil), n.Upstreams...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] || gates[cur] {
			continue
		}
		seen[cur] = true
		cn := g.Node(cur)
		if cn == nil {
			continue
		}
		if len(cn.Upstreams) == 0 {
			return true
		}
		stack = append(stack, cn.Upstreams...)
	}
	return false
}
