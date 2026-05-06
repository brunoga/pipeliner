package task

import (
	"fmt"
	"log/slog"

	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// PluginConfig pairs a plugin name with its configuration block.
type PluginConfig struct {
	Name   string
	Config map[string]any
}

// BuildOption is a functional option for Build.
type BuildOption func(*Task)

// WithLogger sets a custom logger on the task.
func WithLogger(l *slog.Logger) BuildOption {
	return func(t *Task) { t.logger = l }
}

// Build constructs a Task from a name, an ordered list of plugin configs, and a logger.
// db is the shared store for this config; it is forwarded to every plugin factory.
// Plugins are instantiated via the global registry.
func Build(name string, plugins []PluginConfig, db *store.SQLiteStore, logger *slog.Logger, opts ...BuildOption) (*Task, error) {
	if logger == nil {
		logger = slog.Default()
	}
	t := New(name, logger)
	for _, opt := range opts {
		opt(t)
	}
	for _, pc := range plugins {
		desc, ok := plugin.Lookup(pc.Name)
		if !ok {
			return nil, fmt.Errorf("task %q: unknown plugin %q", name, pc.Name)
		}
		if !plugin.IsDispatchable(desc.PluginPhase) {
			return nil, fmt.Errorf("task %q: plugin %q (phase %q) cannot be used as a top-level task plugin; it is only valid inside 'from' or similar sub-plugin lists", name, pc.Name, desc.PluginPhase)
		}
		cfg := pc.Config
		if cfg == nil {
			cfg = map[string]any{}
		}
		impl, err := desc.Factory(cfg, db)
		if err != nil {
			return nil, fmt.Errorf("task %q: plugin %q: %w", name, pc.Name, err)
		}
		t.addPlugin(pluginInstance{desc: desc, impl: impl, config: cfg})
	}
	return t, nil
}
