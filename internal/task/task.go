// Package task wraps executor.Executor as a *Task so that DAG pipelines
// integrate with the scheduler and CLI without any changes to those layers.
package task

import (
	"context"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
)

// Result summarises the outcome of a single pipeline run.
type Result struct {
	Accepted int
	Rejected int
	Failed   int
	Total    int
	Duration time.Duration
	Entries  []*entry.Entry
}

// BuildOption is a functional option applied to a Task after construction.
type BuildOption func(*Task)

// Task is a named DAG pipeline that can be Run.
// All tasks are backed by an executor.Executor; construct with NewFromExecutor.
type Task struct {
	name   string
	exec   executorRunner
	dryRun bool
}

// Name returns the pipeline name.
func (t *Task) Name() string { return t.name }

// Shutdown releases resources held by the underlying executor.
func (t *Task) Shutdown() {
	if t.exec != nil {
		t.exec.Shutdown()
	}
}

// SetDryRun enables or disables dry-run mode. In dry-run mode sinks skip
// their side effects, making the run fully idempotent.
func (t *Task) SetDryRun(v bool) {
	t.dryRun = v
	if t.exec != nil {
		t.exec.SetDryRun(v)
	}
}

// Run executes the DAG pipeline and returns a Result.
func (t *Task) Run(ctx context.Context) (*Result, error) {
	return t.runFromExecutor(ctx)
}
