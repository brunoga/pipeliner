package executor

import (
	"time"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
)

// Result summarises the outcome of a single DAG pipeline run.
type Result struct {
	// NodeResults holds per-node execution details.
	NodeResults map[dag.NodeID]*NodeResult
	// Total is the total number of entries produced by all source nodes.
	Total int
	// Accepted is the number of entries that reached at least one sink.
	Accepted int
	// Rejected is the number of entries filtered out by processor nodes.
	Rejected int
	// Failed is the number of entries that errored in a sink.
	Failed int
	// Duration is the wall-clock time for the full run.
	Duration time.Duration
	// Entries holds all entries that passed through the pipeline (for inspection).
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
