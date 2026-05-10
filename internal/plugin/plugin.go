// Package plugin defines the interfaces every plugin must implement and the
// TaskContext passed to them during execution.
package plugin

import (
	"context"
	"log/slog"
	"slices"

	"github.com/brunoga/pipeliner/internal/entry"
)

// Role identifies a plugin's place in a DAG pipeline.
type Role string

const (
	RoleSource    Role = "source"    // produces entries; no upstream nodes
	RoleProcessor Role = "processor" // transforms entries; has upstream and downstream
	RoleSink      Role = "sink"      // consumes entries; no downstream nodes
)

// Phase identifies which stage of the pipeline a plugin participates in.
// Deprecated: prefer Role for new plugins; Phase is retained for the linear
// task engine and backward compatibility with existing plugin registrations.
type Phase string

const (
	PhaseFrom     Phase = "from"     // sub-plugins used by series/movies/discover; not dispatched by the task engine
	PhaseInput    Phase = "input"
	PhaseMetainfo Phase = "metainfo"
	PhaseFilter   Phase = "filter"
	PhaseModify   Phase = "modify"
	PhaseOutput   Phase = "output"
	PhaseLearn    Phase = "learn"
)

// Phases lists all phases in execution order.
var Phases = []Phase{
	PhaseInput,
	PhaseMetainfo,
	PhaseFilter,
	PhaseModify,
	PhaseOutput,
	PhaseLearn,
}

// IsDispatchable reports whether a phase is dispatched by the task engine.
// PhaseFrom and any future sub-plugin phases are not dispatchable.
func IsDispatchable(p Phase) bool {
	return slices.Contains(Phases, p)
}

// Plugin is the base interface every plugin must satisfy.
type Plugin interface {
	Name() string
	Phase() Phase
}

// TaskContext carries per-execution context to each plugin call.
type TaskContext struct {
	// Name is the task being executed.
	Name string
	// Config is the plugin's configuration block (arbitrary key-value pairs).
	Config map[string]any
	// Logger is a task-scoped logger.
	Logger *slog.Logger
	// DryRun, when true, instructs sink plugins to skip side effects.
	DryRun bool
}

// SourcePlugin generates entries from an external source.
// This is the DAG-native interface. Plugins that register with PhaseInput
// and implement InputPlugin are adapted automatically by the DAG executor.
type SourcePlugin interface {
	Plugin
	Generate(ctx context.Context, tc *TaskContext) ([]*entry.Entry, error)
}

// ProcessorPlugin transforms a batch of entries. The returned slice is passed
// downstream; entries absent from the returned slice are considered filtered
// out. Processors should call e.Reject(reason) on dropped entries so the
// executor can count and report them.
// This is the DAG-native interface. Legacy metainfo/filter/modify plugins are
// adapted automatically by the DAG executor.
type ProcessorPlugin interface {
	Plugin
	Process(ctx context.Context, tc *TaskContext, entries []*entry.Entry) ([]*entry.Entry, error)
}

// SinkPlugin consumes entries and performs side effects (download, notify, persist).
// Sinks check tc.DryRun and skip external side effects when it is true.
// This is the DAG-native interface. Legacy output/learn plugins are adapted
// automatically by the DAG executor.
type SinkPlugin interface {
	Plugin
	Consume(ctx context.Context, tc *TaskContext, entries []*entry.Entry) error
}

// InputPlugin generates entries from an external source.
type InputPlugin interface {
	Plugin
	Run(ctx context.Context, task *TaskContext) ([]*entry.Entry, error)
}

// MetainfoPlugin annotates entries with additional metadata without changing their state.
type MetainfoPlugin interface {
	Plugin
	Annotate(ctx context.Context, task *TaskContext, e *entry.Entry) error
}

// FilterPlugin accepts or rejects entries based on configured criteria.
// The plugin calls e.Accept(), e.Reject(), or leaves the state unchanged.
type FilterPlugin interface {
	Plugin
	Filter(ctx context.Context, task *TaskContext, e *entry.Entry) error
}

// BatchFilterPlugin is an optional extension of FilterPlugin for plugins that
// can process all entries at once more efficiently than one at a time (e.g.
// by firing network requests in parallel). The task engine calls FilterBatch
// instead of Filter for any plugin that implements this interface.
// The plugin must respect already-decided entries (IsRejected/IsFailed) and
// must honour context cancellation.
type BatchFilterPlugin interface {
	Plugin
	FilterBatch(ctx context.Context, task *TaskContext, entries []*entry.Entry) error
}

// ModifyPlugin transforms entry fields without changing acceptance state.
type ModifyPlugin interface {
	Plugin
	Modify(ctx context.Context, task *TaskContext, e *entry.Entry) error
}

// OutputPlugin receives all accepted entries after the modify phase.
type OutputPlugin interface {
	Plugin
	Output(ctx context.Context, task *TaskContext, entries []*entry.Entry) error
}

// LearnPlugin receives only accepted entries after the output phase so it can
// persist decisions (e.g. mark entries as seen or downloaded). The task engine
// pre-filters to accepted before calling Learn; plugins do not need to guard
// against other states.
type LearnPlugin interface {
	Plugin
	Learn(ctx context.Context, task *TaskContext, entries []*entry.Entry) error
}

// BatchMetainfoPlugin is an optional extension of MetainfoPlugin for plugins
// that need to annotate all entries at once (e.g. to fire network requests in
// parallel). The task engine calls AnnotateBatch instead of Annotate for any
// plugin that implements this interface.
type BatchMetainfoPlugin interface {
	Plugin
	AnnotateBatch(ctx context.Context, task *TaskContext, entries []*entry.Entry) error
}

// ShutdownPlugin is an optional interface for plugins that hold resources
// (connections, goroutines, open file handles) that must be released when the
// pipeline is torn down. The task engine calls Shutdown once after all runs
// that use the plugin are complete — at process exit for daemon mode, or after
// the run completes for one-shot mode. It is also called when a config reload
// replaces a task with a new instance.
type ShutdownPlugin interface {
	Plugin
	Shutdown()
}

// SearchPlugin actively searches a source for entries matching a query string.
// SearchPlugins are used as sub-plugins by the discover input plugin and are
// not dispatched directly by the task engine. Register them with PhaseFrom.
type SearchPlugin interface {
	Plugin
	Search(ctx context.Context, task *TaskContext, query string) ([]*entry.Entry, error)
}
