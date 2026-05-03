package web

import (
	"sync"
	"time"
)

const maxRunsPerTask = 20

// RunRecord captures the outcome of one task execution.
type RunRecord struct {
	Task     string
	At       time.Time
	Accepted int
	Rejected int
	Failed   int
	Total    int
	Duration time.Duration
	Err      string // non-empty if the run returned an error
}

// History stores the last maxRunsPerTask records per task.
type History struct {
	mu   sync.Mutex
	runs map[string][]RunRecord
}

// NewHistory creates an empty History.
func NewHistory() *History {
	return &History{runs: make(map[string][]RunRecord)}
}

// Add appends a record, evicting the oldest if the buffer is full.
func (h *History) Add(r RunRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs[r.Task] = append(h.runs[r.Task], r)
	if len(h.runs[r.Task]) > maxRunsPerTask {
		h.runs[r.Task] = h.runs[r.Task][len(h.runs[r.Task])-maxRunsPerTask:]
	}
}

// All returns a snapshot of all records, newest-first per task.
func (h *History) All() map[string][]RunRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string][]RunRecord, len(h.runs))
	for task, runs := range h.runs {
		cp := make([]RunRecord, len(runs))
		copy(cp, runs)
		// reverse so index 0 is the most recent
		for i, j := 0, len(cp)-1; i < j; i, j = i+1, j-1 {
			cp[i], cp[j] = cp[j], cp[i]
		}
		out[task] = cp
	}
	return out
}
