// Package traces persists per-run entry traces (captured by the executor)
// in a capped store bucket so the dashboard's run inspector can answer
// "why didn't it grab X?" after the fact. The last maxRunsPerTask runs are
// kept per task; older traces are evicted on write.
package traces

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/store"
)

// BucketName is the shared bucket holding all run traces.
const BucketName = "run_traces"

// maxRunsPerTask bounds how many traced runs are kept per task.
const maxRunsPerTask = 20

// RunTrace is one persisted run.
type RunTrace struct {
	RunID     string                `json:"run_id"`
	Task      string                `json:"task"`
	At        time.Time             `json:"at"`
	DryRun    bool                  `json:"dry_run"`
	Truncated int                   `json:"truncated,omitempty"`
	Entries   []executor.EntryTrace `json:"entries"`
}

// Meta is the listing form: everything except the entries.
type Meta struct {
	RunID   string    `json:"run_id"`
	At      time.Time `json:"at"`
	DryRun  bool      `json:"dry_run"`
	Entries int       `json:"entries"`
}

// Store persists run traces.
type Store struct{ bucket store.Bucket }

func NewStore(b store.Bucket) *Store { return &Store{bucket: b} }

func runKey(task, runID string) string { return task + "|" + runID }
func indexKey(task string) string      { return task + "|_index" }

// Put stores one run's trace and evicts beyond the per-task cap.
func (s *Store) Put(rt RunTrace) error {
	if err := s.bucket.Put(runKey(rt.Task, rt.RunID), rt); err != nil {
		return err
	}
	var idx []Meta
	if _, err := s.bucket.Get(indexKey(rt.Task), &idx); err != nil {
		return err
	}
	idx = append(idx, Meta{RunID: rt.RunID, At: rt.At, DryRun: rt.DryRun, Entries: len(rt.Entries)})
	for len(idx) > maxRunsPerTask {
		if err := s.bucket.Delete(runKey(rt.Task, idx[0].RunID)); err != nil {
			return err
		}
		idx = idx[1:]
	}
	return s.bucket.Put(indexKey(rt.Task), idx)
}

// List returns the kept runs for a task, newest last.
func (s *Store) List(task string) ([]Meta, error) {
	var idx []Meta
	if _, err := s.bucket.Get(indexKey(task), &idx); err != nil {
		return nil, err
	}
	return idx, nil
}

// Get returns one run's full trace.
func (s *Store) Get(task, runID string) (*RunTrace, error) {
	var rt RunTrace
	found, err := s.bucket.Get(runKey(task, runID), &rt)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("trace not found: %s/%s", task, runID)
	}
	return &rt, nil
}

// Tasks lists every task that has at least one persisted run trace.
func (s *Store) Tasks() ([]string, error) {
	keys, err := s.bucket.Keys()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range keys {
		if task, ok := strings.CutSuffix(k, "|_index"); ok {
			out = append(out, task)
		}
	}
	sort.Strings(out)
	return out, nil
}
