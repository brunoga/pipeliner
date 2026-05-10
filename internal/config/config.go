// Package config loads and validates pipeliner Starlark configuration files.
//
// A config file may use two pipeline styles that can be freely mixed:
//
// Linear (legacy) style — task() + plugin():
//
//	task("tv-shows",
//	    [plugin("rss", url="..."), plugin("seen"), plugin("transmission", host="...")],
//	    schedule="1h")
//
// DAG style — input() / process() / output() / pipeline():
//
//	rss1 = input("rss", url="https://feed1.com/rss")
//	rss2 = input("rss", url="https://feed2.com/rss")
//	seen = process("seen", from_=merge(rss1, rss2))
//	output("transmission", from_=seen, host="localhost")
//	pipeline("tv-shows", schedule="1h")
//
// Both styles support env(name, default=None) for environment variable access
// and load("./file.star", ...) for splitting configs across files.
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
	// Tasks maps task names to their ordered plugin configurations (linear style).
	Tasks map[string][]task.PluginConfig
	// Schedules maps linear task names to schedule expressions.
	Schedules map[string]string
	// Graphs maps pipeline names to their DAG graph (DAG style).
	Graphs map[string]*dag.Graph
	// GraphSchedules maps DAG pipeline names to schedule expressions.
	GraphSchedules map[string]string
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
// have valid configs. For linear tasks, plugins must be dispatchable phases.
// For DAG graphs, the graph structure and field requirements are also checked.
// Returns a list of errors (never nil).
func Validate(c *Config) []error {
	var errs []error

	// Linear task validation.
	for taskName, plugins := range c.Tasks {
		for _, pc := range plugins {
			d, ok := plugin.Lookup(pc.Name)
			if !ok {
				errs = append(errs, fmt.Errorf("task %q: unknown plugin %q", taskName, pc.Name))
				continue
			}
			if !plugin.IsDispatchable(d.PluginPhase) {
				errs = append(errs, fmt.Errorf("task %q: plugin %q (phase %q) cannot be used as a top-level task plugin; use it inside a 'from' list instead", taskName, pc.Name, d.PluginPhase))
				continue
			}
			if d.Validate == nil {
				continue
			}
			cfg := pc.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			for _, err := range d.Validate(cfg) {
				errs = append(errs, fmt.Errorf("task %q plugin %q: %w", taskName, pc.Name, err))
			}
		}
	}

	// DAG graph validation.
	for name, g := range c.Graphs {
		dagErrs := dag.Validate(g, func(pluginName string) (*plugin.Descriptor, bool) {
			return plugin.Lookup(pluginName)
		})
		for _, err := range dagErrs {
			errs = append(errs, fmt.Errorf("pipeline %q: %w", name, err))
		}
		// Per-plugin config validation.
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

	return errs
}

// BuildTasks instantiates all linear tasks and all DAG pipelines defined in
// the config, returning them as a unified slice sorted alphabetically by name.
// db is the shared store forwarded to every plugin factory.
// If logger is nil, slog.Default() is used.
func BuildTasks(c *Config, db *store.SQLiteStore, logger *slog.Logger, opts ...task.BuildOption) ([]*task.Task, error) {
	var tasks []*task.Task

	// --- Linear tasks ---
	linearNames := sortedStringKeys(c.Tasks)
	for _, name := range linearNames {
		t, err := task.Build(name, c.Tasks[name], db, logger, opts...)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}

	// --- DAG pipelines ---
	// Executors are built with dryRun=false; Task.SetDryRun propagates it later,
	// matching how the linear engine handles dry-run via task.SetDryRun.
	graphNames := sortedStringKeys(c.Graphs)
	for _, name := range graphNames {
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
		tasks = append(tasks, task.NewFromExecutor(name, ex))
	}

	// Sort all tasks (linear + DAG) alphabetically.
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name() < tasks[j].Name() })
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
