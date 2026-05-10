package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/brunoga/pipeliner/internal/task"
)

// ConfigToStarlark serializes a Config to canonical flat Starlark source.
// The output uses only task() and plugin() built-in calls — no functions,
// variables, or load() statements. This is the format produced by the visual
// pipeline editor when generating config text.
func ConfigToStarlark(c *Config) string {
	var b strings.Builder

	// Sort task names for deterministic output.
	names := make([]string, 0, len(c.Tasks))
	for name := range c.Tasks {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		if i > 0 {
			b.WriteString("\n\n")
		}
		writeTask(&b, name, c.Tasks[name], c.Schedules[name])
	}
	b.WriteByte('\n')
	return b.String()
}

func writeTask(b *strings.Builder, name string, plugins []task.PluginConfig, schedule string) {
	fmt.Fprintf(b, "task(%s,\n    [\n", starStr(name))
	for _, pc := range plugins {
		b.WriteString("        ")
		writePlugin(b, pc)
		b.WriteString(",\n")
	}
	b.WriteString("    ]")
	if schedule != "" {
		fmt.Fprintf(b, ",\n    schedule=%s", starStr(schedule))
	}
	b.WriteByte(')')
}

func writePlugin(b *strings.Builder, pc task.PluginConfig) {
	if len(pc.Config) == 0 {
		fmt.Fprintf(b, "plugin(%s)", starStr(pc.Name))
		return
	}

	// Use dict form when config has a "from" key (avoids any reserved-word
	// concerns and keeps the nested structure readable).
	if _, hasFrom := pc.Config["from"]; hasFrom {
		fmt.Fprintf(b, "plugin(%s, %s)", starStr(pc.Name), starDict(pc.Config, 0))
		return
	}

	// Kwargs form for simple configs.
	keys := sortedKeys(pc.Config)
	if len(keys) == 1 {
		k := keys[0]
		fmt.Fprintf(b, "plugin(%s, %s=%s)", starStr(pc.Name), k, starVal(pc.Config[k], 0))
		return
	}
	// Multi-key: one kwarg per line, aligned.
	fmt.Fprintf(b, "plugin(%s,\n", starStr(pc.Name))
	indent := "               " // aligns with the opening paren
	for j, k := range keys {
		if j > 0 {
			b.WriteString(indent)
		} else {
			b.WriteString(indent)
		}
		fmt.Fprintf(b, "%s=%s", k, starVal(pc.Config[k], 3))
		if j < len(keys)-1 {
			b.WriteString(",\n")
		}
	}
	b.WriteByte(')')
}

// starVal converts a Go value to its Starlark literal representation.
func starVal(v any, depth int) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		if t == float64(int(t)) {
			return fmt.Sprintf("%d", int(t))
		}
		return fmt.Sprintf("%g", t)
	case string:
		return starStr(t)
	case []string:
		return starStringList(t)
	case []any:
		return starList(t, depth)
	case map[string]any:
		return starDict(t, depth)
	default:
		return fmt.Sprintf("%q", fmt.Sprintf("%v", v))
	}
}

// starStr returns a double-quoted Starlark string literal.
func starStr(s string) string {
	// Use triple-quotes for multiline strings.
	if strings.Contains(s, "\n") {
		return `"""` + s + `"""`
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func starStringList(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = starStr(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func starList(items []any, depth int) string {
	if len(items) == 0 {
		return "[]"
	}
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = starVal(item, depth+1)
	}
	// Short lists on one line; longer/nested lists indented.
	oneLiner := "[" + strings.Join(parts, ", ") + "]"
	if len(oneLiner) <= 60 && !strings.Contains(oneLiner, "\n") {
		return oneLiner
	}
	pad := strings.Repeat("    ", depth+1)
	var b strings.Builder
	b.WriteString("[\n")
	for _, p := range parts {
		fmt.Fprintf(&b, "%s%s,\n", pad, p)
	}
	fmt.Fprintf(&b, "%s]", strings.Repeat("    ", depth))
	return b.String()
}

func starDict(m map[string]any, depth int) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := sortedKeys(m)
	pad := strings.Repeat("    ", depth+1)
	var b strings.Builder
	b.WriteString("{\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s%s: %s,\n", pad, starStr(k), starVal(m[k], depth+1))
	}
	fmt.Fprintf(&b, "%s}", strings.Repeat("    ", depth))
	return b.String()
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
