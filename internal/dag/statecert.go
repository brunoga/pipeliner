package dag

import (
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// stateCertainty tracks which fields are certain on entries currently in each
// of the four entry states at a point in the graph.
//
// The original single-set certainty model carried an implicit assumption that
// rejected and failed entries do not flow downstream — every narrowing rule
// (condition, require, route ports, semantic groups) promotes a field to
// certain on the basis that "entries lacking the field were rejected, so what
// flows past has the field". The swap_state plugin breaks that invariant by
// reviving rejected/failed entries back into the downstream-visible states.
// Per-state tracking restores soundness: each state's bucket records what is
// certain about entries currently in that state, and swap_state simply swaps
// two buckets.
//
// Two pieces of state are tracked:
//
//   - buckets[s] — fields certain on every entry currently in state s.
//   - populated  — bitset of states that may actually contain entries at
//     this point. Empty (unpopulated) buckets are vacuously certain about
//     every field but contribute nothing to a downstream consumer, so the
//     effective-intersection step skips them.
//
// Downstream Requires checks intersect the buckets the consuming node is
// configured to receive (via Descriptor.EffectiveInputStates), giving the
// most conservative answer when a single node sees multiple populated state
// buckets.
type stateCertainty struct {
	buckets   []map[string]bool // length 4, indexed by stateIdx
	populated entry.StateSet
}

// stateIdx maps an entry.State to the array index it occupies in
// stateCertainty.buckets. Keeping this as a tiny switch (rather than using
// State directly as an index) means a State value drifting out of [0,3]
// cannot silently corrupt the structure.
func stateIdx(s entry.State) int {
	switch s {
	case entry.Undecided:
		return 0
	case entry.Accepted:
		return 1
	case entry.Rejected:
		return 2
	case entry.Failed:
		return 3
	}
	return -1
}

// allStates is the canonical iteration order for the four entry states.
var allStates = [4]entry.State{entry.Undecided, entry.Accepted, entry.Rejected, entry.Failed}

// producingStatesFor returns the set of state buckets a node's Produces
// fields should populate. Sources emit Undecided entries; processors and
// sinks transform entries in their EffectiveInputStates and leave others
// alone. EffectiveInputStates defaults to StatesAll for sources, which would
// (incorrectly) propagate a source's Produces into the Rejected/Failed
// buckets and survive a downstream swap_state — hence the explicit
// source-only override.
func producingStatesFor(d *plugin.Descriptor, role plugin.Role) entry.StateSet {
	if role == plugin.RoleSource {
		return entry.StatesUndecidedOnly
	}
	return d.EffectiveInputStates()
}

func newStateCertainty() stateCertainty {
	bs := make([]map[string]bool, 4)
	for i := range bs {
		bs[i] = make(map[string]bool)
	}
	return stateCertainty{buckets: bs}
}

func (sc stateCertainty) get(s entry.State) map[string]bool {
	i := stateIdx(s)
	if i < 0 {
		return nil
	}
	return sc.buckets[i]
}

// copyBucket overwrites the contents of state dst with a copy of state src's
// bucket. Used when a classifier-shaped processor promotes entries from one
// state to another and we want the destination bucket to inherit the source
// state's certainty (entries carry their fields across the transition).
func (sc *stateCertainty) copyBucket(src, dst entry.State) {
	srcI, dstI := stateIdx(src), stateIdx(dst)
	if srcI < 0 || dstI < 0 || srcI == dstI {
		return
	}
	sc.buckets[dstI] = copySet(sc.buckets[srcI])
}

func (sc stateCertainty) copy() stateCertainty {
	out := stateCertainty{buckets: make([]map[string]bool, 4), populated: sc.populated}
	for i := range sc.buckets {
		out.buckets[i] = copySet(sc.buckets[i])
	}
	return out
}

// markPopulated declares that entries may be in any of states at this point.
// Used when a source emits entries, when a filter newly rejects entries
// (Rejected becomes populated), and so on.
func (sc *stateCertainty) markPopulated(states entry.StateSet) {
	sc.populated |= states
}

// addAll adds the listed fields to every bucket in (states ∩ populated).
// Callers that need to also mark new states as populated (e.g. sources
// originating entries) must call markPopulated explicitly first. This split
// lets a default processor's Produces propagate only to the states actually
// populated upstream, instead of spuriously creating empty Accepted buckets
// for pipelines with no Accept filter upstream.
func (sc *stateCertainty) addAll(states entry.StateSet, fields ...string) {
	for _, s := range allStates {
		if !states.Has(s) || !sc.populated.Has(s) {
			continue
		}
		i := stateIdx(s)
		for _, f := range fields {
			sc.buckets[i][f] = true
		}
	}
}

// swap exchanges the bucket and the populated bit for state a with those of
// state b. This is the validator-side counterpart of the swap_state plugin:
// entries flipped from a→b carry their old (a-state) certainty into the new
// (b-state) bucket, and vice versa, and the populated-state set follows.
func (sc *stateCertainty) swap(a, b entry.State) {
	ai, bi := stateIdx(a), stateIdx(b)
	if ai < 0 || bi < 0 || ai == bi {
		return
	}
	sc.buckets[ai], sc.buckets[bi] = sc.buckets[bi], sc.buckets[ai]
	aBit := entry.StateBit(a)
	bBit := entry.StateBit(b)
	aHad := sc.populated&aBit != 0
	bHad := sc.populated&bBit != 0
	sc.populated &^= aBit | bBit
	if aHad {
		sc.populated |= bBit
	}
	if bHad {
		sc.populated |= aBit
	}
}

// intersect computes the per-state intersection of two stateCertainty values,
// used at merge nodes where each upstream contributes its own per-state view
// and the merged result is what is certain on every path.
//
// populated is the UNION of the inputs: a state is reachable after the merge
// if any upstream could deliver entries in that state. For states populated
// by only one upstream the intersection of "all fields" (vacuous) with that
// upstream's bucket is just that upstream's bucket — captured here by
// treating an unpopulated upstream as vacuously certain.
func (sc stateCertainty) intersect(other stateCertainty) stateCertainty {
	out := stateCertainty{buckets: make([]map[string]bool, 4),
		populated: sc.populated | other.populated}
	for _, s := range allStates {
		i := stateIdx(s)
		switch {
		case sc.populated.Has(s) && other.populated.Has(s):
			m := make(map[string]bool)
			for f := range sc.buckets[i] {
				if other.buckets[i][f] {
					m[f] = true
				}
			}
			out.buckets[i] = m
		case sc.populated.Has(s):
			out.buckets[i] = copySet(sc.buckets[i])
		case other.populated.Has(s):
			out.buckets[i] = copySet(other.buckets[i])
		default:
			out.buckets[i] = make(map[string]bool)
		}
	}
	return out
}

// effective returns the intersection of bucket contents across the states in
// the requested set that are also populated. Unpopulated states are
// vacuously certain about every field and so are skipped — including them
// would force the result to ∅ whenever a consumer's InputStates names a
// state that this node never produces entries for.
//
// Returns an empty (non-nil) map if no populated states intersect with the
// requested set.
func (sc stateCertainty) effective(states entry.StateSet) map[string]bool {
	var out map[string]bool
	first := true
	for _, s := range allStates {
		if !states.Has(s) || !sc.populated.Has(s) {
			continue
		}
		bucket := sc.get(s)
		if first {
			out = copySet(bucket)
			first = false
			continue
		}
		for f := range out {
			if !bucket[f] {
				delete(out, f)
			}
		}
	}
	if out == nil {
		return make(map[string]bool)
	}
	return out
}

// narrowAcceptedUndecided promotes fields into the Accepted and Undecided
// buckets (the entries that pass through a filter) while intersecting the
// Rejected bucket against the certainty of the newly-rejected entries
// (which lack the promoted fields). This is the shared shape of condition
// accept rules, condition reject-absence rules, and require narrowing —
// each promotes some set of fields on the passing branch, and the only thing
// that changes is which fields we know are absent on the rejected branch.
//
// rejectedField, if non-empty, is the field whose absence caused new
// rejections; it is subtracted from the newly-rejected bucket so a
// downstream swap_state cannot revive entries claiming the field is certain.
//
// The Rejected bucket is also marked populated, since the narrowing
// describes a filter that creates new rejections.
func (sc *stateCertainty) narrowAcceptedUndecided(promotes []string, rejectedField string) {
	if len(promotes) == 0 {
		return
	}
	// Build the "newly rejected" certainty: intersection of incoming Accepted
	// and Undecided buckets — but only for buckets that are populated. If a
	// state is unpopulated, no entries from that state can become newly
	// rejected, so it should not constrain the intersection.
	var newlyRejected map[string]bool
	switch {
	case sc.populated.Has(entry.Accepted) && sc.populated.Has(entry.Undecided):
		newlyRejected = make(map[string]bool)
		acc := sc.get(entry.Accepted)
		und := sc.get(entry.Undecided)
		for f := range acc {
			if und[f] {
				newlyRejected[f] = true
			}
		}
	case sc.populated.Has(entry.Accepted):
		newlyRejected = copySet(sc.get(entry.Accepted))
	case sc.populated.Has(entry.Undecided):
		newlyRejected = copySet(sc.get(entry.Undecided))
	default:
		// Neither passing-state is populated — no entries can be filtered,
		// so there is nothing to do.
		return
	}
	if rejectedField != "" {
		delete(newlyRejected, rejectedField)
	}

	// Promote into the passing buckets and mark them populated.
	for _, f := range promotes {
		if sc.populated.Has(entry.Accepted) {
			sc.buckets[stateIdx(entry.Accepted)][f] = true
		}
		if sc.populated.Has(entry.Undecided) {
			sc.buckets[stateIdx(entry.Undecided)][f] = true
		}
	}

	// Intersect Rejected with the newly-rejected certainty. If Rejected was
	// previously unpopulated, the newly-rejected pool simply becomes the
	// Rejected bucket; otherwise we take the intersection across the two
	// sources of rejected entries.
	if sc.populated.Has(entry.Rejected) {
		rej := sc.get(entry.Rejected)
		for f := range rej {
			if !newlyRejected[f] {
				delete(rej, f)
			}
		}
	} else {
		sc.buckets[stateIdx(entry.Rejected)] = newlyRejected
		sc.populated |= entry.StateBit(entry.Rejected)
	}
}

// removeFromAcceptedUndecided is the dual of narrowAcceptedUndecided: a
// reject-presence rule ("reject when x is set") guarantees that entries
// flowing through Accepted/Undecided do NOT have the field, so we strip it
// from those buckets. The Rejected bucket gains nothing certain from this
// rule alone, so we leave it untouched (we don't know what else was on the
// newly-rejected entries beyond having the field).
func (sc *stateCertainty) removeFromAcceptedUndecided(fields []string) {
	for _, f := range fields {
		delete(sc.buckets[stateIdx(entry.Accepted)], f)
		delete(sc.buckets[stateIdx(entry.Undecided)], f)
	}
}
