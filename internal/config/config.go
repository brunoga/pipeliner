// Package config loads and validates pipeliner Starlark configuration files.
//
// A config file is a Starlark script that calls the task() built-in to register
// one or more pipeline tasks. The built-in functions available to scripts are:
//
//	plugin(name, config_dict)        — create a plugin config
//	plugin(name, key=val, ...)       — same, using keyword arguments
//	task(name, plugins, schedule="") — register a task (and optional schedule)
//	env(name, default=None)          — read an environment variable
//
// Example config.star:
//
//	smtp_host = "smtp.example.com"
//	smtp_pass = env("SMTP_PASS")
//
//	def tvdb_enrich():
//	    return [
//	        plugin("metainfo_tvdb", api_key=env("TVDB_KEY"), cache_ttl="12h"),
//	        plugin("require", fields=["enriched"]),
//	    ]
//
//	task("tv-shows",
//	    [plugin("rss", url="https://feeds.example.com/tv")] + tvdb_enrich() + [
//	        plugin("series", static=["Breaking Bad"], tracking="follow", quality="720p+"),
//	        plugin("deluge", host="localhost", password="secret"),
//	    ],
//	    schedule="1h")
package config

import (
	"fmt"
	"log/slog"
	"os"
	"sort"

	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/task"
)

// Config is the result of evaluating a Starlark config file.
type Config struct {
	// Tasks maps task names to their ordered plugin configurations.
	Tasks map[string][]task.PluginConfig
	// Schedules maps task names to schedule expressions ("1h", "0 * * * *").
	Schedules map[string]string
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

// Validate checks that all plugins referenced in the config are registered,
// dispatchable, and have valid config. Returns a list of errors (never nil).
func Validate(c *Config) []error {
	var errs []error
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
	return errs
}

// BuildTasks instantiates all tasks defined in the config and returns them
// sorted alphabetically by name. db is the shared store forwarded to every
// plugin factory. If logger is nil, slog.Default() is used.
func BuildTasks(c *Config, db *store.SQLiteStore, logger *slog.Logger, opts ...task.BuildOption) ([]*task.Task, error) {
	names := make([]string, 0, len(c.Tasks))
	for name := range c.Tasks {
		names = append(names, name)
	}
	sort.Strings(names)

	tasks := make([]*task.Task, 0, len(names))
	for _, name := range names {
		t, err := task.Build(name, c.Tasks[name], db, logger, opts...)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}
