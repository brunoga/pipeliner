// Package config loads and validates pipeliner YAML configuration files.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/task"
	"github.com/brunoga/pipeliner/internal/yaml"
)

// Config is the top-level structure of a pipeliner configuration file.
type Config struct {
	// Tasks maps task names to their ordered plugin configurations.
	Tasks map[string]TaskDef `json:"tasks"`
	// Schedules maps task names to schedule expressions ("1h", "0 * * * *").
	Schedules map[string]string `json:"schedules,omitempty"`
	// Templates defines reusable named plugin config blocks for use: expansion.
	Templates map[string]TemplateDef `json:"templates,omitempty"`
	// Variables defines string substitution values used in config values.
	Variables map[string]string `json:"variables,omitempty"`
}

// taskEntry is a single plugin-name / raw-JSON-config pair.
type taskEntry struct {
	name string
	raw  json.RawMessage
}

// TaskDef is an ordered list of plugin entries (YAML sequence).
// Each item is a single-key mapping: { plugin_name: config }.
// Special items: { use: [...] } or { use: "template_name" } expand a template inline.
// Special items: { priority: N } and { schedule: "..." } are handled as metadata.
type TaskDef []taskEntry

// UnmarshalJSON reads a JSON array of single-key objects into d.
// Each element must be a JSON object with exactly one key.
func (d *TaskDef) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return fmt.Errorf("taskdef: expected array, got %T(%v)", tok, tok)
	}
	*d = nil
	for dec.More() {
		// Each element must be a single-key object.
		var elem map[string]json.RawMessage
		if err := dec.Decode(&elem); err != nil {
			return fmt.Errorf("taskdef: element must be an object: %w", err)
		}
		if len(elem) != 1 {
			return fmt.Errorf("taskdef: each list item must have exactly one key, got %d", len(elem))
		}
		for k, v := range elem {
			*d = append(*d, taskEntry{k, v})
		}
	}
	_, err = dec.Token() // consume ']'
	return err
}

// MarshalJSON writes d as a JSON array of single-key objects.
func (d TaskDef) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, e := range d {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('{')
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
		buf.WriteByte('}')
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// TemplateDef is an ordered collection of plugin entries stored as a JSON object.
// Unlike TaskDef, templates are YAML mappings, not sequences.
// The reserved key "params" holds the parameter name list and is not a plugin.
type TemplateDef []taskEntry

// UnmarshalJSON reads a JSON object into t, preserving key order.
func (t *TemplateDef) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("templatedef: expected object, got %T", tok)
	}
	*t = nil
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("templatedef: expected string key, got %T", tok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		*t = append(*t, taskEntry{key, raw})
	}
	_, err = dec.Token() // consume '}'
	return err
}

// MarshalJSON writes t as a JSON object with keys in insertion order.
func (t TemplateDef) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range t {
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

// get returns the raw JSON for the given name in t, or (nil, false) if absent.
func (t TemplateDef) get(name string) (json.RawMessage, bool) {
	for _, e := range t {
		if e.name == name {
			return e.raw, true
		}
	}
	return nil, false
}

// params returns the declared parameter names for this template, or nil if
// the template has no "params" key.
func (t TemplateDef) params() ([]string, error) {
	raw, ok := t.get("params")
	if !ok {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, fmt.Errorf("template params must be a list of strings: %w", err)
	}
	return names, nil
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

	// Validate template plugin names (skip "params" which is reserved metadata).
	for tmplName, def := range c.Templates {
		for _, e := range def {
			if e.name == "params" {
				continue // reserved key, not a plugin
			}
			if _, ok := plugin.Lookup(e.name); !ok {
				errs = append(errs, fmt.Errorf("template %q: unknown plugin %q", tmplName, e.name))
			}
		}
	}

	for taskName, def := range c.Tasks {
		expanded, err := expandTaskDef(taskName, def, c.Templates)
		if err != nil {
			errs = append(errs, fmt.Errorf("task %q: %w", taskName, err))
			continue
		}
		for _, e := range expanded {
			if e.name == "priority" || e.name == "schedule" {
				continue
			}
			d, ok := plugin.Lookup(e.name)
			if !ok {
				errs = append(errs, fmt.Errorf("task %q: unknown plugin %q", taskName, e.name))
				continue
			}
			if d.PluginPhase == plugin.PhaseFrom {
				errs = append(errs, fmt.Errorf("task %q: plugin %q is a from plugin; use it via 'series.from', 'movies.from', or 'discover.from' instead", taskName, e.name))
				continue
			}
			if d.Validate == nil {
				continue
			}
			cfg := normalizePluginCfg(e.raw)
			for _, err := range d.Validate(cfg) {
				errs = append(errs, fmt.Errorf("task %q plugin %q: %w", taskName, e.name, err))
			}
		}
	}

	return errs
}

// BuildTasks instantiates all tasks defined in the config. Template expansion is
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
		expanded, err := expandTaskDef(name, def, c.Templates)
		if err != nil {
			return nil, err
		}
		priority := extractPriority(&expanded)
		pcs, err := taskDefToPluginConfigs(name, expanded)
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

// expandTaskDef iterates over the task's entries and expands any "use:" entries
// inline, replacing them with the plugin entries from the named template.
// Non-use entries are passed through unchanged.
func expandTaskDef(taskName string, def TaskDef, templates map[string]TemplateDef) ([]taskEntry, error) {
	var result []taskEntry
	for _, e := range def {
		if e.name != "use" {
			result = append(result, e)
			continue
		}
		expanded, err := expandUse(taskName, e.raw, templates)
		if err != nil {
			return nil, err
		}
		result = append(result, expanded...)
	}
	return result, nil
}

// expandUse parses a "use:" value, looks up the named template, validates the
// argument count matches the template's params, applies param substitution to
// each plugin entry in the template, and returns the expanded entries.
//
// Accepted forms:
//
//	use: "template_name"                        — zero-param template
//	use: [template_name, arg1, …]               — positional args mapped to params in order
//	use:                                        — named args (required for multiline values)
//	  template: template_name
//	  param1: value1
//	  param2: |
//	    multiline value
func expandUse(taskName string, raw json.RawMessage, templates map[string]TemplateDef) ([]taskEntry, error) {
	// String form: zero-param shorthand.
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return expandUseWithArgs(taskName, name, nil, templates)
	}

	// Array form: [name, arg1, arg2, ...] — positional params.
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err == nil {
		if len(items) == 0 {
			return nil, fmt.Errorf("task %q: 'use' array must not be empty", taskName)
		}
		if err := json.Unmarshal(items[0], &name); err != nil {
			return nil, fmt.Errorf("task %q: first element of 'use' array must be a string template name", taskName)
		}
		return expandUseWithArgs(taskName, name, items[1:], templates)
	}

	// Object form: {template: name, param1: val1, ...} — named params.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		nameRaw, ok := obj["template"]
		if !ok {
			return nil, fmt.Errorf("task %q: 'use' object must have a 'template' key", taskName)
		}
		if err := json.Unmarshal(nameRaw, &name); err != nil {
			return nil, fmt.Errorf("task %q: 'use.template' must be a string", taskName)
		}
		delete(obj, "template")
		return expandUseWithNamedArgs(taskName, name, obj, templates)
	}

	return nil, fmt.Errorf("task %q: 'use' must be a string, array, or object", taskName)
}

// expandUseWithArgs looks up the template, validates arg count, and applies
// param substitution to each plugin entry, returning the expanded entries.
func expandUseWithArgs(taskName, tmplName string, args []json.RawMessage, templates map[string]TemplateDef) ([]taskEntry, error) {
	tmpl, ok := templates[tmplName]
	if !ok {
		return nil, fmt.Errorf("task %q: unknown template %q", taskName, tmplName)
	}

	paramNames, err := tmpl.params()
	if err != nil {
		return nil, fmt.Errorf("task %q: template %q: %w", taskName, tmplName, err)
	}

	if len(args) != len(paramNames) {
		return nil, fmt.Errorf("task %q: template %q expects %d arg(s) (%s), got %d",
			taskName, tmplName, len(paramNames), strings.Join(paramNames, ", "), len(args))
	}

	params := make(map[string]json.RawMessage, len(paramNames))
	for i, pname := range paramNames {
		params[pname] = args[i]
	}
	return applyTemplateParams(tmpl, params)
}

// expandUseWithNamedArgs looks up the template, validates that all declared
// params are provided (and no unknown keys are present), and returns the
// expanded entries with param substitution applied.
func expandUseWithNamedArgs(taskName, tmplName string, args map[string]json.RawMessage, templates map[string]TemplateDef) ([]taskEntry, error) {
	tmpl, ok := templates[tmplName]
	if !ok {
		return nil, fmt.Errorf("task %q: unknown template %q", taskName, tmplName)
	}

	paramNames, err := tmpl.params()
	if err != nil {
		return nil, fmt.Errorf("task %q: template %q: %w", taskName, tmplName, err)
	}

	// All declared params must be provided.
	for _, p := range paramNames {
		if _, ok := args[p]; !ok {
			return nil, fmt.Errorf("task %q: use %q: missing parameter %q", taskName, tmplName, p)
		}
	}
	// No undeclared params allowed.
	declared := make(map[string]bool, len(paramNames))
	for _, p := range paramNames {
		declared[p] = true
	}
	for k := range args {
		if !declared[k] {
			return nil, fmt.Errorf("task %q: use %q: unexpected parameter %q", taskName, tmplName, k)
		}
	}

	params := make(map[string]json.RawMessage, len(args))
	maps.Copy(params, args)
	return applyTemplateParams(tmpl, params)
}

// applyTemplateParams expands a template's entries using the given params map.
func applyTemplateParams(tmpl TemplateDef, params map[string]json.RawMessage) ([]taskEntry, error) {
	var result []taskEntry
	for _, e := range tmpl {
		if e.name == "params" {
			continue
		}
		result = append(result, taskEntry{e.name, applyParams(e.raw, params)})
	}
	return result, nil
}

// applyParams replaces {$ key $} placeholders in raw using the raw JSON values
// from params. Two substitution modes are used depending on context:
//
//   - Whole-string: when "{$ key $}" is the entire JSON string value, the
//     quoted string is replaced by the raw arg. This allows list and object
//     args (e.g. indexers: ["a","b"]).
//
//   - Embedded: when the placeholder appears within a longer string, the arg
//     is JSON-escaped and inserted in-place (safe for multiline/special chars).
func applyParams(raw json.RawMessage, params map[string]json.RawMessage) json.RawMessage {
	s := string(raw)
	for k, argRaw := range params {
		placeholder := "{$ " + k + " $}"

		// Whole-string replacement: replace the entire "..." JSON string with
		// the raw arg value — allows lists, objects, and any JSON type.
		wholeStr := `"` + placeholder + `"`
		s = strings.ReplaceAll(s, wholeStr, string(argRaw))

		// Embedded replacement: placeholder appears inside a larger string.
		// JSON-encode the arg's string value so the result stays valid JSON.
		if strings.Contains(s, placeholder) {
			var str string
			if json.Unmarshal(argRaw, &str) == nil {
				encoded, _ := json.Marshal(str)
				s = strings.ReplaceAll(s, placeholder, string(encoded[1:len(encoded)-1]))
			} else {
				s = strings.ReplaceAll(s, placeholder, string(argRaw))
			}
		}
	}
	return json.RawMessage(s)
}

// extractPriority reads the optional "priority" entry from an expanded entry
// slice, removes it, and returns its integer value. Returns 0 if absent.
func extractPriority(entries *[]taskEntry) int {
	for i, e := range *entries {
		if e.name == "priority" {
			*entries = append((*entries)[:i], (*entries)[i+1:]...)
			var p int
			if json.Unmarshal(e.raw, &p) == nil {
				return p
			}
			var pf float64
			if json.Unmarshal(e.raw, &pf) == nil {
				return int(pf)
			}
			return 0
		}
	}
	return 0
}

// normalizePluginCfg converts raw plugin JSON into a map[string]any.
// If the raw value is not a JSON object (e.g. an array or scalar), it is
// wrapped as {"value": <scalar>} — the same convention used by plugin
// factories that support non-map config syntax (e.g. the movies inline list).
func normalizePluginCfg(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}
	}
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) == nil {
		return cfg
	}
	var scalar any
	if json.Unmarshal(raw, &scalar) == nil {
		return map[string]any{"value": scalar}
	}
	return map[string]any{}
}

// taskDefToPluginConfigs converts an expanded []taskEntry into the ordered
// PluginConfig slice expected by task.Build. Plugins are returned in config-file
// order; "priority" and "schedule" entries are skipped (they are metadata).
func taskDefToPluginConfigs(_ string, entries []taskEntry) ([]task.PluginConfig, error) {
	var pcs []task.PluginConfig
	for _, e := range entries {
		if e.name == "priority" || e.name == "schedule" {
			continue
		}
		pcs = append(pcs, task.PluginConfig{Name: e.name, Config: normalizePluginCfg(e.raw)})
	}
	return pcs, nil
}
