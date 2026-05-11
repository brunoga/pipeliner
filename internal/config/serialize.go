package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// ConfigToStarlark serializes a Config to canonical DAG Starlark source using
// input() / process() / output() / pipeline() built-in calls. Sink nodes
// (role=sink) are not assigned to variables; source and processor nodes are
// assigned using their node ID as the variable name.
func ConfigToStarlark(c *Config) string {
	var b strings.Builder
	names := sortedStringKeys(c.Graphs)
	for i, name := range names {
		if i > 0 {
			b.WriteString("\n\n")
		}
		writeGraph(&b, name, c.Graphs[name], c.GraphSchedules[name])
	}
	b.WriteByte('\n')
	return b.String()
}

func writeGraph(b *strings.Builder, name string, g *dag.Graph, schedule string) {
	for _, n := range g.Nodes() {
		d, _ := plugin.Lookup(n.PluginName)
		role := plugin.RoleProcessor
		if d != nil {
			role = d.EffectiveRole()
		}
		fromStr := upstreamsToStar(n.Upstreams)
		cfgStr := configToKwargsStr(n.Config)

		switch role {
		case plugin.RoleSource:
			args := []string{starStr(n.PluginName)}
			if cfgStr != "" {
				args = append(args, cfgStr)
			}
			fmt.Fprintf(b, "%s = input(%s)\n", n.ID, strings.Join(args, ", "))
		case plugin.RoleProcessor:
			parts := []string{starStr(n.PluginName)}
			if fromStr != "" {
				parts = append(parts, "from_="+fromStr)
			}
			if cfgStr != "" {
				parts = append(parts, cfgStr)
			}
			fmt.Fprintf(b, "%s = process(%s)\n", n.ID, strings.Join(parts, ", "))
		default: // sink
			parts := []string{starStr(n.PluginName)}
			if fromStr != "" {
				parts = append(parts, "from_="+fromStr)
			}
			if cfgStr != "" {
				parts = append(parts, cfgStr)
			}
			fmt.Fprintf(b, "output(%s)\n", strings.Join(parts, ", "))
		}
	}
	if schedule != "" {
		fmt.Fprintf(b, "pipeline(%s, schedule=%s)", starStr(name), starStr(schedule))
	} else {
		fmt.Fprintf(b, "pipeline(%s)", starStr(name))
	}
}

func upstreamsToStar(ups []dag.NodeID) string {
	if len(ups) == 0 {
		return ""
	}
	if len(ups) == 1 {
		return string(ups[0])
	}
	ids := make([]string, len(ups))
	for i, u := range ups {
		ids[i] = string(u)
	}
	return "merge(" + strings.Join(ids, ", ") + ")"
}

func configToKwargsStr(cfg map[string]any) string {
	if len(cfg) == 0 {
		return ""
	}
	keys := sortedKeys(cfg)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := cfg[k]
		if v == nil || v == "" {
			continue
		}
		parts = append(parts, k+"="+starVal(v, 0))
	}
	return strings.Join(parts, ", ")
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

func starStr(s string) string {
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
