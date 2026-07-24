package executor

import "github.com/brunoga/pipeliner/internal/entry"

// Trace capture: the executor records, per entry, which node changed its
// state and why, so the dashboard's run inspector can answer "why didn't it
// grab X?" without a log dive. Capture is bounded (maxTracedEntries per run)
// and always on — dry-runs especially, since dry-run + inspector is the
// config-debugging loop.

// maxTracedEntries bounds per-run trace memory. Runs bigger than this keep
// executing normally; TracesTruncated reports how many entries went untraced.
const maxTracedEntries = 500

// TraceStep is one state change at one node.
type TraceStep struct {
	Node   string `json:"node"`
	State  string `json:"state"` // emitted, accepted, rejected, failed, consumed
	Reason string `json:"reason,omitempty"`
}

// EntryTrace is the full journey of one entry through the DAG.
type EntryTrace struct {
	Title  string      `json:"title"`
	URL    string      `json:"url"`
	Final  string      `json:"final"`
	Reason string      `json:"reason,omitempty"`
	Steps  []TraceStep `json:"steps"`
}

type traceRecorder struct {
	limit     int
	order     []*entry.Entry
	traces    map[*entry.Entry]*EntryTrace
	truncated int
}

func newTraceRecorder(limit int) *traceRecorder {
	return &traceRecorder{limit: limit, traces: make(map[*entry.Entry]*EntryTrace)}
}

// stateLabel maps an entry's current condition onto the inspector vocabulary.
func stateLabel(e *entry.Entry) string {
	if e.IsConsumed() {
		return "consumed"
	}
	switch e.State {
	case entry.Accepted:
		return "accepted"
	case entry.Rejected:
		return "rejected"
	case entry.Failed:
		return "failed"
	default:
		return "undecided"
	}
}

// reasonOf returns the reason matching the entry's current state.
func reasonOf(e *entry.Entry) string {
	switch e.State {
	case entry.Accepted:
		return e.AcceptReason
	case entry.Rejected:
		return e.RejectReason
	case entry.Failed:
		return e.FailReason
	}
	return ""
}

// observeEmitted registers entries first produced by node.
func (r *traceRecorder) observeEmitted(node string, entries []*entry.Entry) {
	for _, e := range entries {
		if _, ok := r.traces[e]; ok {
			continue
		}
		if len(r.order) >= r.limit {
			r.truncated++
			continue
		}
		t := &EntryTrace{
			Title: e.Title, URL: e.URL,
			Steps: []TraceStep{{Node: node, State: stateLabel(e), Reason: reasonOf(e)}},
		}
		r.traces[e] = t
		r.order = append(r.order, e)
	}
}

// snapshot captures the pre-node condition of tracked entries.
func (r *traceRecorder) snapshot(entries []*entry.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		if _, ok := r.traces[e]; ok {
			out[i] = stateLabel(e) + "\x00" + reasonOf(e)
		}
	}
	return out
}

// observeChanged appends a step for every tracked entry whose condition
// changed while node ran. before must come from snapshot() on the same slice.
func (r *traceRecorder) observeChanged(node string, entries []*entry.Entry, before []string) {
	for i, e := range entries {
		t, ok := r.traces[e]
		if !ok || i >= len(before) || before[i] == "" {
			continue
		}
		cur := stateLabel(e) + "\x00" + reasonOf(e)
		if cur == before[i] {
			continue
		}
		t.Steps = append(t.Steps, TraceStep{Node: node, State: stateLabel(e), Reason: reasonOf(e)})
	}
}

// finalize stamps final states and returns the traces in emission order.
func (r *traceRecorder) finalize() []EntryTrace {
	out := make([]EntryTrace, 0, len(r.order))
	for _, e := range r.order {
		t := r.traces[e]
		t.Final = stateLabel(e)
		t.Reason = reasonOf(e)
		out = append(out, *t)
	}
	return out
}
