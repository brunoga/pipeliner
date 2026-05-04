// Package config loads and validates pipeliner YAML configuration files.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/task"
	"github.com/brunoga/pipeliner/internal/yaml"
)

// Config is the top-level structure of a pipeliner configuration file.
type Config struct {
	// Tasks maps task names to their plugin configurations.
	Tasks map[string]TaskDef `json:"tasks"`
	// Schedules maps task names to schedule expressions ("1h", "0 * * * *").
	Schedules map[string]string `json:"schedules,omitempty"`
	// Templates defines reusable plugin config blocks merged into tasks.
	Templates map[string]TaskDef `json:"templates,omitempty"`
	// Variables defines string substitution values used in config values.
	Variables map[string]string `json:"variables,omitempty"`
}

// taskEntry is a single plugin-name / raw-JSON-config pair within a TaskDef.
type taskEntry struct {
	name string
	raw  json.RawMessage
}

// TaskDef is an ordered sequence of plugin name → raw JSON config pairs.
// Order matches the config file and determines plugin execution order within
// each phase.
type TaskDef []taskEntry

// UnmarshalJSON reads a JSON object into d, preserving key order.
func (d *TaskDef) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("taskdef: expected object, got %T", tok)
	}
	*d = nil
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("taskdef: expected string key, got %T", tok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		*d = append(*d, taskEntry{key, raw})
	}
	_, err = dec.Token() // consume '}'
	return err
}

// MarshalJSON writes d as a JSON object with keys in insertion order.
func (d TaskDef) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range d {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(e.name)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		if e.raw == nil {
			buf.WriteString("null")
		} else {
			buf.Write(e.raw)
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// get returns the raw JSON for the given name, or (nil, false) if absent.
func (d TaskDef) get(name string) (json.RawMessage, bool) {
	for _, e := range d {
		if e.name == name {
			return e.raw, true
		}
	}
	return nil, false
}

// set adds or updates the entry for name. If name already exists, its value
// is updated in place; otherwise a new entry is appended.
func (d *TaskDef) set(name string, raw json.RawMessage) {
	for i, e := range *d {
		if e.name == name {
			(*d)[i].raw = raw
			return
		}
	}
	*d = append(*d, taskEntry{name, raw})
}

// delete removes the entry for name. It is a no-op if name is absent.
func (d *TaskDef) delete(name string) {
	for i, e := range *d {
		if e.name == name {
			*d = append((*d)[:i], (*d)[i+1:]...)
			return
		}
	}
}

// Load reads and parses a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}
	return parse(data)
}

// ParseBytes parses a YAML config from a byte slice.
// Useful in tests and when config is loaded from a source other than a file.
func ParseBytes(data []byte) (*Config, error) {
	return parse(data)
}

func parse(data []byte) (*Config, error) {
	// First pass: extract variables only.
	var partial struct {
		Variables map[string]string `json:"variables"`
	}
	if err := yaml.Unmarshal(data, &partial); err != nil {
		return nil, fmt.Errorf("config: parse variables: %w", err)
	}

	// Substitute {$ key $} and ${ENV_VAR} tokens throughout the raw YAML bytes, then re-parse.
	var subErr error
	data, subErr = applyVariables(data, partial.Variables)
	if subErr != nil {
		return nil, subErr
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	if c.Tasks == nil {
		c.Tasks = map[string]TaskDef{}
	}
	return &c, nil
}

// Validate checks that all plugin names referenced in the config are registered
// and returns a list of validation errors (never nil, may be empty).
func Validate(c *Config) []error {
	var errs []error
	for taskName, def := range c.Tasks {
		for _, e := range def {
			if e.name == "template" || e.name == "priority" || e.name == "schedule" {
				continue // meta keys, not plugin names
			}
			d, ok := plugin.Lookup(e.name)
			if !ok {
				errs = append(errs, fmt.Errorf("task %q: unknown plugin %q", taskName, e.name))
				continue
			}
			if d.PluginPhase == plugin.PhaseSearch {
				errs = append(errs, fmt.Errorf("task %q: plugin %q is a search plugin; use it via 'discover.via' instead", taskName, e.name))
			}
		}
	}
	for tmplName, def := range c.Templates {
		for _, e := range def {
			if e.name == "template" {
				continue // meta key, not a plugin name
			}
			if _, ok := plugin.Lookup(e.name); !ok {
				errs = append(errs, fmt.Errorf("template %q: unknown plugin %q", tmplName, e.name))
			}
		}
	}
	return errs
}

// BuildTasks instantiates all tasks defined in the config. Template merging is
// applied before plugin instantiation. Tasks are returned sorted by priority
// (ascending); tasks with equal priority are sorted alphabetically by name.
// db is the shared store for this config and is forwarded to every plugin factory.
// If logger is nil, slog.Default() is used for each task.
func BuildTasks(c *Config, db *store.SQLiteStore, logger *slog.Logger, opts ...task.BuildOption) ([]*task.Task, error) {
	type built struct {
		t        *task.Task
		priority int
		name     string
	}
	var items []built

	for name, def := range c.Tasks {
		merged, err := mergeTemplate(name, def, c.Templates)
		if err != nil {
			return nil, err
		}
		priority := extractPriority(&merged)
		pcs, err := taskDefToPluginConfigs(name, merged)
		if err != nil {
			return nil, err
		}
		t, err := task.Build(name, pcs, db, logger, opts...)
		if err != nil {
			return nil, err
		}
		items = append(items, built{t: t, priority: priority, name: name})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].priority != items[j].priority {
			return items[i].priority < items[j].priority
		}
		return items[i].name < items[j].name
	})

	tasks := make([]*task.Task, len(items))
	for i, b := range items {
		tasks[i] = b.t
	}
	return tasks, nil
}

// extractPriority reads the optional top-level "priority" key from a TaskDef
// and removes it so it is not treated as a plugin name. Returns 0 if absent.
func extractPriority(def *TaskDef) int {
	raw, ok := def.get("priority")
	if !ok {
		return 0
	}
	def.delete("priority")
	var p int
	if json.Unmarshal(raw, &p) == nil {
		return p
	}
	var pf float64
	if json.Unmarshal(raw, &pf) == nil {
		return int(pf)
	}
	return 0
}

// resolveTemplate recursively resolves a template by name, handling template
// inheritance. The stack parameter tracks the current resolution chain to detect
// circular references.
func resolveTemplate(name string, templates map[string]TaskDef, stack []string) (TaskDef, error) {
	// Check for circular references.
	if slices.Contains(stack, name) {
		return nil, fmt.Errorf("template %q: circular reference detected: %s -> %s", stack[0], strings.Join(stack, " -> "), name)
	}

	tmpl, ok := templates[name]
	if !ok {
		return nil, fmt.Errorf("unknown template %q", name)
	}

	// Check if this template itself references parent templates.
	parentRaw, hasParent := tmpl.get("template")
	if !hasParent {
		return tmpl, nil
	}

	// Parse parent template names.
	var parentNames []string
	var single string
	if json.Unmarshal(parentRaw, &single) == nil {
		parentNames = []string{single}
	} else if json.Unmarshal(parentRaw, &parentNames) != nil {
		return nil, fmt.Errorf("template %q: 'template' must be a string or array of strings", name)
	}

	// Resolve and merge parent templates left-to-right.
	newStack := append(stack, name)
	merged := make(TaskDef, 0)
	for _, parentName := range parentNames {
		resolved, err := resolveTemplate(parentName, templates, newStack)
		if err != nil {
			return nil, fmt.Errorf("template %q: %w", name, err)
		}
		for _, e := range resolved {
			merged.set(e.name, e.raw)
		}
	}

	// Apply this template's own keys on top (same merge rules as task→template).
	for _, e := range tmpl {
		if e.name == "template" {
			continue
		}
		if strings.HasSuffix(e.name, "!") {
			base := e.name[:len(e.name)-1]
			merged.delete(base)
			merged.set(base, e.raw)
			continue
		}
		existing, hasExisting := merged.get(e.name)
		if !hasExisting {
			merged.set(e.name, e.raw)
			continue
		}
		merged.set(e.name, mergeJSON(existing, e.raw))
	}
	return merged, nil
}

// mergeTemplate returns a new TaskDef that is the template's def merged with
// the task's def.
//
// Merge rules:
//   - Meta keys ("template", "priority", "schedule") are handled separately.
//   - A task key ending in "!" (e.g. "regexp!") explicitly replaces the
//     template value for the base key ("regexp"), ignoring any template default.
//   - When both template and task define the same plugin key and both values
//     are JSON objects, the objects are shallowly merged (task fields win on
//     conflict). This lets a task extend a template's plugin config.
//   - For all other cases, the task value replaces the template value.
func mergeTemplate(taskName string, def TaskDef, templates map[string]TaskDef) (TaskDef, error) {
	raw, ok := def.get("template")
	if !ok {
		return def, nil
	}

	// "template" may be a string or []string
	var names []string
	var single string
	if json.Unmarshal(raw, &single) == nil {
		names = []string{single}
	} else if json.Unmarshal(raw, &names) != nil {
		return nil, fmt.Errorf("task %q: 'template' must be a string or array of strings", taskName)
	}

	merged := make(TaskDef, 0)
	for _, tmplName := range names {
		resolved, err := resolveTemplate(tmplName, templates, nil)
		if err != nil {
			return nil, fmt.Errorf("task %q: %w", taskName, err)
		}
		for _, e := range resolved {
			merged.set(e.name, e.raw)
		}
	}

	// Apply task keys. Keys ending in "!" override the corresponding base key
	// (stripping the "!"). For other keys, merge JSON objects shallowly.
	for _, e := range def {
		if e.name == "template" {
			continue
		}
		if strings.HasSuffix(e.name, "!") {
			base := e.name[:len(e.name)-1]
			merged.delete(base)
			merged.set(base, e.raw)
			continue
		}
		existing, hasExisting := merged.get(e.name)
		if !hasExisting {
			merged.set(e.name, e.raw)
			continue
		}
		// Both template and task define this key. Shallow-merge if both are objects.
		merged.set(e.name, mergeJSON(existing, e.raw))
	}
	return merged, nil
}

// mergeJSON shallow-merges two JSON values. If both are objects, task fields
// win on conflict. Otherwise the task value (b) replaces the template value (a).
func mergeJSON(a, b json.RawMessage) json.RawMessage {
	var aMap, bMap map[string]json.RawMessage
	if json.Unmarshal(a, &aMap) != nil || json.Unmarshal(b, &bMap) != nil {
		return b // not both objects; task wins
	}
	maps.Copy(aMap, bMap) // bMap fields overwrite aMap fields
	out, err := json.Marshal(aMap)
	if err != nil {
		return b
	}
	return out
}

// taskDefToPluginConfigs converts a TaskDef into the ordered PluginConfig slice
// expected by task.Build. Plugins are returned in config-file order; within each
// phase, that order is preserved during execution.
func taskDefToPluginConfigs(taskName string, def TaskDef) ([]task.PluginConfig, error) {
	var pcs []task.PluginConfig
	for _, e := range def {
		if e.name == "template" || e.name == "priority" || e.name == "schedule" {
			continue
		}
		var cfg map[string]any
		if len(e.raw) > 0 && string(e.raw) != "null" {
			if err := json.Unmarshal(e.raw, &cfg); err != nil {
				// scalar config (e.g. boolean true for a flag plugin)
				var scalar any
				if err2 := json.Unmarshal(e.raw, &scalar); err2 != nil {
					return nil, fmt.Errorf("task %q plugin %q: %w", taskName, e.name, err)
				}
				cfg = map[string]any{"value": scalar}
			}
		}
		if cfg == nil {
			cfg = map[string]any{}
		}
		pcs = append(pcs, task.PluginConfig{Name: e.name, Config: cfg})
	}
	return pcs, nil
}
