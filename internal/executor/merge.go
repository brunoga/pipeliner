package executor

import "github.com/brunoga/pipeliner/internal/entry"

// mergeAndDedup concatenates entry slices from multiple upstream nodes and
// deduplicates by URL (first-seen wins, matching the linear engine behaviour).
func mergeAndDedup(slices ...[]*entry.Entry) []*entry.Entry {
	seen := make(map[string]bool)
	var out []*entry.Entry
	for _, s := range slices {
		for _, e := range s {
			if seen[e.URL] {
				continue
			}
			seen[e.URL] = true
			out = append(out, e)
		}
	}
	return out
}

// cloneAll returns a deep copy of each entry in the slice. Used when a node's
// output fans out to more than one downstream consumer so that each branch
// gets its own mutable copy.
func cloneAll(entries []*entry.Entry) []*entry.Entry {
	out := make([]*entry.Entry, len(entries))
	for i, e := range entries {
		out[i] = e.Clone()
	}
	return out
}
