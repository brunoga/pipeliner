// Package task wraps executor.Executor as a *Task so that DAG pipelines
// integrate with the scheduler and CLI without any changes to those layers.
package task

import (
	"context"
	"github.com/brunoga/pipeliner/internal/executor"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
)

// Result summarises the outcome of a single pipeline run.
type Result struct {
	RunID           string
	Traces          []executor.EntryTrace
	TracesTruncated int
	Accepted        int
	Rejected        int
	Failed          int
	Undecided       int
	Total           int
	Duration        time.Duration
	Entries         []*entry.Entry
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
// their side effects and the commit phase is skipped entirely, making the
// run fully idempotent (no tracker state advances).
func (t *Task) SetDryRun(v bool) {
	t.dryRun = v
	if t.exec != nil {
		t.exec.SetDryRun(v)
	}
}

// DryRun reports whether the task is currently in dry-run mode.
func (t *Task) DryRun() bool { return t.dryRun }

// RunDryRun temporarily enables dry-run for a single Run call and restores the
// previous setting on return. Callers can use this for per-trigger dry-run
// overrides without disturbing the task's persistent flag. The daemon's
// per-task concurrency guard means there's no concurrent SetDryRun racing
// against this method for the same task.
func (t *Task) RunDryRun(ctx context.Context) (*Result, error) {
	prev := t.dryRun
	t.SetDryRun(true)
	defer t.SetDryRun(prev)
	return t.Run(ctx)
}

// SetValidateFields enables per-entry field validation before each node runs.
func (t *Task) SetValidateFields(v bool) {
	if t.exec != nil {
		t.exec.SetValidateFields(v)
	}
}

// Run executes the DAG pipeline and returns a Result.
func (t *Task) Run(ctx context.Context) (*Result, error) {
	return t.runFromExecutor(ctx)
}
