package dag

import (
	"fmt"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
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
// Field certainty is tracked per entry state (Accepted, Undecided, Rejected,
// Failed): each bucket records which fields are guaranteed on entries
// currently in that state. The original single-set model assumed rejected
// and failed entries never reached downstream nodes — an invariant the
// swap_state plugin breaks by reviving them — so the validator now intersects
// the buckets that a consuming node is configured to receive (per
// Descriptor.EffectiveInputStates) before checking Requires.
//
// "Reachable" fields are potentially present: union of upstreams, plus
// MayProduce. A Requires group satisfied only by reachable-but-not-certain
// fields emits a warning (merge gap or conditional MayProduce upstream).
//
// Returns (errors, warnings). Errors block pipeline load; warnings are advisory.
func Validate(g *Graph, reg func(name string) (*plugin.Descriptor, bool)) (errs, warnings []error) {
	// Topological sort catches cycles and provides processing order.
	layers, err := g.Layers()
	if err != nil {
		return []error{err}, nil
	}

	// Per-node field sets built up as we process layers in order.
	// reachable: union of all fields that might appear (Produces + MayProduce
	//            from any path).
	// certain:   per-state buckets of fields guaranteed on entries in each
	//            state at the output of this node.
	reachable := make(map[NodeID]map[string]bool, g.Len())
	certain := make(map[NodeID]stateCertainty, g.Len())

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

			// Build reachable and per-state certain sets for this node from
			// its upstreams.
			reach, cert := inheritReachAndCert(n, reachable, certain, g, reg)

			// Infer field contracts from the port's accept expression.
			acceptExpr, _ := n.Config["_port_accept_expr"].(string)
			applyPortAcceptNarrowingStateful(acceptExpr, reach, &cert)

			// Check Requires groups against reachable and the intersection of
			// certain buckets across this node's effective input states. If
			// none of those states are populated upstream (the node would
			// never receive entries at runtime), fall back to the union of
			// populated buckets so the Requires check doesn't fire on a
			// structurally unreachable node — a real "no Accept filter"
			// upstream is already caught elsewhere, and emitting an extra
			// "may not be present" here would just be noise.
			inStates := d.EffectiveInputStates()
			effCert := cert.effective(inStates)
			if len(effCert) == 0 && cert.populated != 0 && cert.populated&inStates == 0 {
				effCert = cert.effective(cert.populated)
			}
			for _, group := range d.Requires {
				reachFound, certFound := false, false
				for _, f := range group {
					if reach[f] {
						reachFound = true
					}
					if effCert[f] {
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

			// Warn on references to deprecated fields anywhere in this node's
			// surface: Requires groups, route port expression, condition rules,
			// require fields, and pattern-typed schema entries (e.g. pathfmt's
			// path=). Static-only — runtime e.Get/Set calls aren't checked.
			deprecationWarnings(n, d, &warnings)

			// Warn on accept-only condition nodes — they don't reject the
			// non-matching rest, which is almost always the user's intent.
			if w := conditionMissingRejectWarning(n, d); w != nil {
				warnings = append(warnings, w)
			}

			// Add this node's Produces / MayProduce. Produces are guaranteed
			// on the buckets corresponding to the states entries can occupy
			// at the node's output: sources emit Undecided entries, while
			// processors mutate the states they process (their
			// EffectiveInputStates) and leave others alone. MayProduce only
			// affects the reachable union.
			producingStates := producingStatesFor(d, role)
			// Sources originate entries — explicitly mark their producing
			// states populated. Processors with non-empty Produces are
			// classifier-shaped (series/movies/premiere): their Produces
			// describes the fields they guarantee on Accepted entries, so we
			// also mark their producing states populated and inherit the
			// upstream Undecided certainty into the freshly-populated
			// Accepted bucket — matching entries flow Undecided→Accepted and
			// carry whatever fields they already had. Processors without
			// Produces pass entries through and only inherit populated
			// states from upstream — they must not spuriously populate
			// Accepted in pipelines with no Accept filter.
			if role == plugin.RoleSource {
				cert.markPopulated(producingStates)
			} else if len(d.Produces) > 0 {
				if producingStates.Has(entry.Accepted) &&
					!cert.populated.Has(entry.Accepted) &&
					cert.populated.Has(entry.Undecided) {
					cert.copyBucket(entry.Undecided, entry.Accepted)
				}
				cert.markPopulated(producingStates)
			}
			for _, f := range d.Produces {
				reach[f] = true
				cert.addAll(producingStates, f)
			}
			for _, f := range d.MayProduce {
				reach[f] = true
			}

			// For condition nodes: apply narrowing from the rule expressions so
			// that the Validate() field-certainty analysis matches what the UI
			// shows.  accept rules promote fields; reject rules either remove or
			// promote depending on whether they use presence or absence ops.
			if n.PluginName == "condition" {
				applyConditionNarrowingStateful(n.Config, reach, &cert)
			}

			// For require nodes: every listed field is guaranteed non-empty on
			// the output (entries missing any of them are rejected), so promote
			// each one from reachable to certain on the passing buckets and
			// intersect the rejected bucket against the newly-rejected
			// certainty.
			if n.PluginName == "require" {
				applyRequireNarrowingStateful(n.Config, reach, &cert)
			}

			// For swap_state nodes: exchange the two state buckets named in
			// the swap config. This makes downstream Requires checks see the
			// (typically smaller) certainty of the revived state, restoring
			// soundness when rejected/failed entries are revived back into
			// the downstream-visible states.
			if n.PluginName == "swap_state" {
				applySwapStateNarrowing(n.Config, &cert)
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
							cert.addAll(producingStates, f)
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

// inheritReachAndCert builds the reach (union) and per-state certain sets a
// node sees from its upstreams: single inheritance for the 0/1-upstream
// cases, per-state intersection for merges (with the same route-group
// special-case as the legacy single-set model — currently intersection in
// both branches; routegroup is preserved for future divergence).
func inheritReachAndCert(n *Node, reachable map[NodeID]map[string]bool, certain map[NodeID]stateCertainty,
	g *Graph, reg func(string) (*plugin.Descriptor, bool)) (map[string]bool, stateCertainty) {

	switch len(n.Upstreams) {
	case 0:
		return make(map[string]bool), newStateCertainty()
	case 1:
		upID := n.Upstreams[0]
		return copySet(reachable[upID]), certain[upID].copy()
	}

	// Merge (N>1 upstreams): union for reachable.
	reach := make(map[string]bool)
	for _, upID := range n.Upstreams {
		for f := range reachable[upID] {
			reach[f] = true
		}
	}

	// Certain: per-state intersection across upstreams. The route-group case
	// is currently identical (intersection) — kept distinguishable for future
	// per-state divergence (e.g. per-port masking before merge).
	_ = sharedRouteGroup(n.Upstreams, g, reg) // detection retained; behaviour identical for now

	var cert stateCertainty
	for i, upID := range n.Upstreams {
		if i == 0 {
			cert = certain[upID].copy()
			continue
		}
		cert = cert.intersect(certain[upID])
	}
	return reach, cert
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

// applyConditionNarrowingStateful applies condition-expression narrowing to
// the per-state certainty being built by Validate(). It mirrors the
// client-side logic in visual-editor.js so server-side field warnings are
// consistent with what the condition builder UI shows.
//
// Accept rules promote fields into the passing buckets (Accepted, Undecided)
// and intersect the Rejected bucket against the newly-rejected certainty
// (without the promoted field, since absent entries are what triggered the
// new rejection). Reject rules use the same shape — absence-op promotes;
// presence-op removes from the passing buckets.
func applyConditionNarrowingStateful(config map[string]any, reach map[string]bool, cert *stateCertainty) {
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

	// Use the Accepted bucket as the "currently certain" reference for
	// narrowing decisions — by construction Accepted and Undecided are kept
	// in lockstep through every promotion, so this is the field set the
	// expression evaluator should see.
	keys := func(m map[string]bool) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out
	}
	reachSlice := func() []string { return keys(reach) }
	certSlice := func() []string { return keys(cert.get(entry.Accepted)) }

	for _, r := range rules {
		switch r.ruleType {
		case "accept":
			promoted := NarrowCertain(r.expr, certSlice(), reachSlice())
			for _, f := range promoted {
				reach[f] = true
			}
			// Accept rule promotes f → newly-rejected entries are the ones
			// that did NOT match (so they may lack f). No specific field
			// drives the rejection here; just intersect Rejected against
			// the incoming Accepted∩Undecided certainty (unchanged by
			// these promotions yet).
			cert.narrowAcceptedUndecided(promoted, "")
		case "reject":
			removed := RejectPresenceRemoved(r.expr, reachSlice())
			for _, f := range removed {
				delete(reach, f)
			}
			cert.removeFromAcceptedUndecided(removed)

			promoted := RejectAbsencePromoted(r.expr, certSlice(), reachSlice())
			for _, f := range promoted {
				reach[f] = true
			}
			// Reject-absence: rejection criterion is "f is absent". The
			// newly-rejected entries provably lack f, so subtract f from the
			// newly-rejected certainty. Only the first promoted field per
			// rule is treated as the absence target (rules with multi-field
			// absence checks are rare and conservative behaviour is fine).
			var absent string
			if len(promoted) > 0 {
				absent = promoted[0]
			}
			cert.narrowAcceptedUndecided(promoted, absent)
		}
	}
}

// applyRequireNarrowingStateful promotes the fields listed in a require
// node's config to certain on the passing (Accepted, Undecided) buckets,
// and intersects the Rejected bucket against the newly-rejected certainty
// (which lacks the required fields). The require filter rejects entries
// missing any listed field; downstream nodes can assume each is present
// unless a swap_state revives the rejected entries — in which case the
// shrunk Rejected bucket carries the correct "may not be present" signal
// through the swap.
func applyRequireNarrowingStateful(config map[string]any, reach map[string]bool, cert *stateCertainty) {
	fields := requireFields(config)
	if len(fields) == 0 {
		return
	}
	for _, f := range fields {
		reach[f] = true
	}
	// Use the first field as the absence anchor — newly-rejected entries
	// lack at least one of the listed fields, and treating the first as
	// definitely-absent is a sound under-approximation (the others may also
	// be absent; we just don't claim it).
	cert.narrowAcceptedUndecided(fields, fields[0])
}

// applySwapStateNarrowing exchanges the two state buckets named in the
// swap_state node's config. Parse failures fall through silently — the
// plugin's own Validate already reports them, and an unparseable config
// shouldn't produce a misleading downstream warning.
func applySwapStateNarrowing(config map[string]any, cert *stateCertainty) {
	raw, ok := config["swap"]
	if !ok {
		return
	}
	names := plugin.ToStringSlice(raw)
	if len(names) != 2 {
		return
	}
	a, ok := parseStateName(names[0])
	if !ok {
		return
	}
	b, ok := parseStateName(names[1])
	if !ok {
		return
	}
	cert.swap(a, b)
}

func parseStateName(s string) (entry.State, bool) {
	switch s {
	case "accepted":
		return entry.Accepted, true
	case "rejected":
		return entry.Rejected, true
	case "failed":
		return entry.Failed, true
	case "undecided":
		return entry.Undecided, true
	}
	return 0, false
}

// requireFields extracts the field names declared in a require node's "fields"
// config. Accepts a single string or a list of strings; silently skips other
// shapes (the plugin's own Validate already reports them).
func requireFields(config map[string]any) []string {
	v, ok := config["fields"]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
