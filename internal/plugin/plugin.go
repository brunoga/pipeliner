// Package plugin defines the interfaces every plugin must implement and the
// TaskContext passed to them during execution.
package plugin

import (
	"context"
	"log/slog"

	"github.com/brunoga/pipeliner/internal/entry"
)

// Role identifies a plugin's place in a DAG pipeline.
type Role string

const (
	RoleSource    Role = "source"    // produces entries; no upstream nodes
	RoleProcessor Role = "processor" // transforms entries; has upstream and downstream
	RoleSink      Role = "sink"      // consumes entries; no downstream nodes
)

// Phase is a legacy identifier kept for plugins that still declare it.
// All new plugin code should use Role instead.
type Phase string

const (
	// PhaseFrom identifies from-plugins used as dynamic title sources by
	// series, movies, and discover. These plugins implement SourcePlugin.
	PhaseFrom     Phase = "from"
	PhaseInput    Phase = "input"
	PhaseMetainfo Phase = "metainfo"
	PhaseFilter   Phase = "filter"
	PhaseModify   Phase = "modify"
	PhaseOutput   Phase = "output"
	PhaseLearn    Phase = "learn"
)

// Plugin is the base interface every plugin must satisfy.
type Plugin interface {
	Name() string
}

// TaskContext carries per-execution context to each plugin call.
type TaskContext struct {
	// Name is the pipeline being executed.
	Name string
	// Config is the plugin's configuration block.
	Config map[string]any
	// Logger is a pipeline-scoped logger.
	Logger *slog.Logger
	// DryRun, when true, instructs sink plugins to skip side effects.
	DryRun bool
}

// SourcePlugin generates entries from an external source.
type SourcePlugin interface {
	Plugin
	Generate(ctx context.Context, tc *TaskContext) ([]*entry.Entry, error)
}

// ProcessorPlugin transforms a batch of entries. The returned slice is passed
// downstream; entries absent from the returned slice are considered filtered
// out. Processors should call e.Reject(reason) on dropped entries so the
// executor can count and report them.
type ProcessorPlugin interface {
	Plugin
	Process(ctx context.Context, tc *TaskContext, entries []*entry.Entry) ([]*entry.Entry, error)
}

// SinkPlugin consumes entries and performs side effects (download, notify, persist).
// Sinks must check tc.DryRun and skip external side effects when it is true.
type SinkPlugin interface {
	Plugin
	Consume(ctx context.Context, tc *TaskContext, entries []*entry.Entry) error
}

// ShutdownPlugin is an optional interface for plugins that hold resources
// (connections, goroutines, file handles) that must be released when the
// pipeline is torn down. Shutdown is called once after all runs that use the
// plugin are complete.
type ShutdownPlugin interface {
	Plugin
	Shutdown()
}

// CommitPlugin is an optional interface for processors that must persist
// state only after all downstream sinks have confirmed success.
// The executor calls Commit once after all sink nodes have run, passing
// only the entries this processor accepted that were not failed by any
// downstream sink (across all fan-out branches, matched by URL).
type CommitPlugin interface {
	Plugin
	Commit(ctx context.Context, tc *TaskContext, entries []*entry.Entry) error
}

// SearchPlugin actively searches a source for entries matching a query string.
// SearchPlugins are used as via-plugins by the discover processor.
type SearchPlugin interface {
	Plugin
	Search(ctx context.Context, tc *TaskContext, query string) ([]*entry.Entry, error)
}
