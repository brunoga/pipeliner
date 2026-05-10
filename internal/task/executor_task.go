package task

// executor_task.go bridges the DAG executor into the Task type so DAG pipelines
// work everywhere a *Task is expected (scheduler, CLI run/daemon commands).

import (
	"context"

	"github.com/brunoga/pipeliner/internal/executor"
)

// executorRunner is the interface the task delegates to when backed by a DAG executor.
type executorRunner interface {
	Name() string
	Run(ctx context.Context) (*executor.Result, error)
	SetDryRun(v bool)
	Shutdown()
}

// NewFromExecutor wraps a DAG executor as a *Task so it can be used wherever a
// *Task is expected without changing the scheduler or CLI.
func NewFromExecutor(name string, ex executorRunner) *Task {
	return &Task{name: name, exec: ex}
}

// IsDryRun reports whether dry-run mode is enabled.
func (t *Task) IsDryRun() bool { return t.dryRun }

// runFromExecutor delegates Run to the underlying DAG executor and adapts the
// executor.Result into a task.Result.
func (t *Task) runFromExecutor(ctx context.Context) (*Result, error) {
	r, err := t.exec.Run(ctx)
	if err != nil {
		return nil, err
	}
	return &Result{
		Accepted: r.Accepted,
		Rejected: r.Rejected,
		Failed:   r.Failed,
		Total:    r.Total,
		Duration: r.Duration,
		Entries:  r.Entries,
	}, nil
}
