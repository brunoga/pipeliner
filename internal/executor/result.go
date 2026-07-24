package executor

import (
	"time"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
)

// Result summarises the outcome of a single DAG pipeline run.
//
// Counter semantics: the executor walks every entry that flowed across an edge
// in the DAG, deduplicates by URL (with the pointer as a fallback key for
// empty-URL entries), and excludes entries that were consumed as input by a
// Descriptor.ReplacesUpstream plugin. For each remaining group the strongest
// state observed across all clones (Accepted > Rejected > Failed > Undecided)
// is the one counted. This gives correct numbers for two patterns the old
// per-source-entry counter got wrong:
//
//   - Fan-out: clones change state on non-branch-0 paths; the per-URL aggregate
//     picks that up.
//   - discover-style processors that emit brand new entries from search
//     backends: the upstream source entries used as search candidates are
//     excluded, and the new entries (with their own URLs) are what get counted.
type Result struct {
	// RunID is the short hex token identifying this run in logs and traces.
	RunID string
	// Traces holds the per-entry, per-node state-change journey for up to
	// maxTracedEntries entries (emission order). TracesTruncated counts
	// entries beyond the cap that executed untraced.
	Traces          []EntryTrace
	TracesTruncated int
	// NodeResults holds per-node execution details.
	NodeResults map[dag.NodeID]*NodeResult
	// Total is the number of distinct entries (by URL or pointer) the
	// executor's counter considered after ReplacesUpstream discards.
	Total int
	// Accepted is the number of distinct entries that ended up Accepted in at
	// least one branch.
	Accepted int
	// Rejected is the number of distinct entries that were rejected somewhere
	// in the pipeline and never made it to Accepted on any branch.
	Rejected int
	// Failed is the number of distinct entries that failed and were not
	// recovered by an accept on a different branch.
	Failed int
	// Undecided is the remainder — distinct entries that never received a
	// terminal state on any branch (passed through the DAG silently).
	Undecided int
	// Duration is the wall-clock time for the full run.
	Duration time.Duration
	// Entries holds one representative entry per distinct group, in the same
	// dedup order the counter used.
	Entries []*entry.Entry
}

// NodeResult holds the execution stats for a single graph node.
type NodeResult struct {
	// In is the number of entries received from upstream.
	In int
	// Out is the number of entries passed downstream (0 for sinks).
	Out int
	// Dropped is In-Out for processor nodes (entries filtered out).
	Dropped int
	// Err is the first error returned by the plugin, if any.
	Err error
}
