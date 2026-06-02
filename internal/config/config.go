// Package config loads and validates pipeliner DAG pipeline configuration files.
//
// Config files are Starlark scripts that call input(), process(), output(),
// and pipeline() to define pipelines. env(name, default=None) reads
// environment variables. load() splits configs across files.
//
// Example:
//
//	src    = input("rss", url="https://example.com/rss")
//	seen   = process("seen", upstream=src)
//	output("transmission", upstream=seen, host="localhost")
//	pipeline("my-pipeline", schedule="1h")
package config

import (
	"fmt"
	"log/slog"
	"os"
	"sort"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/task"
)

// Config is the result of evaluating a Starlark config file.
type Config struct {
	// Graphs maps pipeline names to their DAG graph.
	Graphs map[string]*dag.Graph
	// GraphOrder lists pipeline names in source order — the order in which the
	// corresponding pipeline("name", …) calls appear in the config. The
	// dashboard, scheduler, and visual editor iterate this slice so they all
	// show pipelines in the same order as the text config. If empty (e.g.
	// programmatically built Config), callers fall back to alphabetical.
	GraphOrder []string
	// GraphSchedules maps pipeline names to schedule expressions ("1h", "0 * * * *").
	GraphSchedules map[string]string
	// UserFunctions holds the user-defined pipeline functions discovered in the
	// source, keyed by function name.
	UserFunctions map[string]*UserFunctionDef
	// FunctionCalls holds the function call invocations per pipeline, keyed by
	// pipeline name then call key.
	FunctionCalls map[string][]*FunctionCallRecord
}

// Load reads and executes a Starlark configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}
	c, err := execute(path, data)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// ParseBytes executes a Starlark config from a byte slice.
// Useful in tests and for the web UI's live validator.
func ParseBytes(data []byte) (*Config, error) {
	c, err := execute("<input>", data)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// Validate checks that all plugins referenced in the config are registered and
// have valid configs. Returns (errors, warnings); errors block loading,
// warnings are advisory (e.g. merge-gap or MayProduce field risks).
func Validate(c *Config) (errs, warnings []error) {
	for name, g := range c.Graphs {
		dagErrs, dagWarnings := dag.Validate(g, func(pluginName string) (*plugin.Descriptor, bool) {
			return plugin.Lookup(pluginName)
		})
		for _, err := range dagErrs {
			errs = append(errs, fmt.Errorf("pipeline %q: %w", name, err))
		}
		for _, w := range dagWarnings {
			warnings = append(warnings, fmt.Errorf("pipeline %q: %w", name, w))
		}
		for _, n := range g.Nodes() {
			d, ok := plugin.Lookup(n.PluginName)
			if !ok || d.Validate == nil {
				continue
			}
			cfg := n.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			for _, err := range d.Validate(cfg) {
				errs = append(errs, fmt.Errorf("pipeline %q node %q plugin %q: %w", name, n.ID, n.PluginName, err))
			}
		}
	}
	return errs, warnings
}

// BuildTasks instantiates all DAG pipelines and returns them as []*task.Task
// in the source order recorded by c.GraphOrder (the order pipeline("name", …)
// calls appear in the config). Falls back to alphabetical for callers that
// build a Config programmatically without populating GraphOrder. db is the
// shared store forwarded to every plugin factory. If logger is nil,
// slog.Default() is used.
func BuildTasks(c *Config, db *store.SQLiteStore, logger *slog.Logger, opts ...task.BuildOption) ([]*task.Task, error) {
	names := orderedNames(c)
	tasks := make([]*task.Task, 0, len(names))
	for _, name := range names {
		g := c.Graphs[name]
		plugins := make(map[dag.NodeID]*executor.PluginInstance, g.Len())
		for _, n := range g.Nodes() {
			d, ok := plugin.Lookup(n.PluginName)
			if !ok {
				return nil, fmt.Errorf("pipeline %q: unknown plugin %q", name, n.PluginName)
			}
			cfg := n.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			impl, err := d.Factory(cfg, db)
			if err != nil {
				return nil, fmt.Errorf("pipeline %q: node %q: %w", name, n.ID, err)
			}
			plugins[n.ID] = &executor.PluginInstance{Desc: d, Impl: impl, Config: cfg}
		}
		ex := executor.New(name, g, plugins, db, logger, false)
		t := task.NewFromExecutor(name, ex)
		for _, opt := range opts {
			opt(t)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// orderedNames returns the pipeline names in source order. If GraphOrder is
// populated it is used as-is, with a defensive append of any Graphs key that
// somehow isn't represented (shouldn't happen for configs from execute, but
// keeps callers that mutate Graphs after parsing safe). If GraphOrder is
// empty (programmatic Config builders), falls back to alphabetical.
func orderedNames(c *Config) []string {
	if len(c.GraphOrder) == 0 {
		return sortedStringKeys(c.Graphs)
	}
	seen := make(map[string]bool, len(c.GraphOrder))
	names := make([]string, 0, len(c.Graphs))
	for _, n := range c.GraphOrder {
		if _, ok := c.Graphs[n]; ok && !seen[n] {
			names = append(names, n)
			seen[n] = true
		}
	}
	// Append any graphs not listed in GraphOrder (alphabetically) so we never
	// silently drop a pipeline if order tracking missed it.
	extra := make([]string, 0)
	for name := range c.Graphs {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(names, extra...)
}
