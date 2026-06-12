package executor

import (
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
)

// stateRank orders states by "strength" — higher rank wins when multiple clones
// of the same entry land in different states across branches. Accepted is
// strongest because the user wants the counter to reflect that something
// happened to the entry on at least one branch (matches the user-facing
// pipeline-done summary); Rejected beats Failed beats Undecided.
func stateRank(s entry.State) int {
	switch s {
	case entry.Accepted:
		return 3
	case entry.Rejected:
		return 2
	case entry.Failed:
		return 1
	default: // Undecided
		return 0
	}
}

// aggregateCounters walks every entry the executor saw, deduplicates by URL
// (with the entry pointer as a fallback key for URL-less entries), excludes
// pointers in discarded, and returns the counter totals plus a representative
// entry per group in stable iteration order. See Result doc for semantics.
func aggregateCounters(
	sourceEntries []*entry.Entry,
	edges map[edgeKey][]*entry.Entry,
	discarded map[*entry.Entry]bool,
) (total, accepted, rejected, failed, undecided int, entries []*entry.Entry) {
	// Per group: best (highest-rank) state, plus a representative entry so we
	// can re-publish a stable Entries slice. order preserves first-seen order
	// so the representative slice matches the order the counter walked.
	bestState := make(map[string]entry.State)
	repr := make(map[string]*entry.Entry)
	var order []string

	consider := func(e *entry.Entry) {
		if e == nil || discarded[e] {
			return
		}
		key := e.URL
		if key == "" {
			key = fmt.Sprintf("__ptr:%p", e)
		}
		if _, seen := bestState[key]; !seen {
			bestState[key] = e.State
			repr[key] = e
			order = append(order, key)
			return
		}
		if stateRank(e.State) > stateRank(bestState[key]) {
			bestState[key] = e.State
			repr[key] = e
		}
	}

	// Walk source entries first so the representative slice leads with sources
	// in their generation order.
	for _, e := range sourceEntries {
		consider(e)
	}
	// Then every entry that flowed across an edge. We iterate edges in
	// NodeID-sorted (from, to) order so the slice is deterministic for tests.
	keys := make([]edgeKey, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sortEdgeKeys(keys)
	for _, k := range keys {
		for _, e := range edges[k] {
			consider(e)
		}
	}

	entries = make([]*entry.Entry, 0, len(order))
	for _, k := range order {
		entries = append(entries, repr[k])
		switch bestState[k] {
		case entry.Accepted:
			accepted++
		case entry.Rejected:
			rejected++
		case entry.Failed:
			failed++
		default:
			undecided++
		}
	}
	total = len(order)
	return
}

// sortEdgeKeys orders edge keys lexicographically by (from, to). Inline sort
// keeps the executor package free of an extra import.
func sortEdgeKeys(keys []edgeKey) {
	less := func(a, b edgeKey) bool {
		if a.from != b.from {
			return string(a.from) < string(b.from)
		}
		return string(a.to) < string(b.to)
	}
	// Insertion sort — fine for the small N of node graph edges and avoids
	// pulling sort.Slice in just for this helper.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && less(keys[j], keys[j-1]); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}

