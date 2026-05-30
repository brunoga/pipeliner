package dag

import (
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/expr"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// deprecationWarnings appends advisory warnings for any reference to a
// deprecated entry field on this node. Static-only: it inspects the descriptor
// and the Starlark config, never any runtime e.Get / e.Set call.
//
// The same (field, location) is reported at most once per node so that, for
// example, listing a deprecated field twice in a Requires group doesn't double
// the noise.
func deprecationWarnings(n *Node, d *plugin.Descriptor, warnings *[]error) {
	seen := map[string]bool{}
	emit := func(name, where string) {
		key := name + "@" + where
		if seen[key] {
			return
		}
		seen[key] = true
		meta, ok := entry.LookupField(name)
		if !ok || !meta.Deprecated {
			return
		}
		msg := fmt.Sprintf("node %q (plugin %q): %s references deprecated field %q",
			n.ID, n.PluginName, where, name)
		switch {
		case meta.ReplacedBy != "":
			msg += fmt.Sprintf(" — use %q instead", meta.ReplacedBy)
		case meta.DeprecationNote != "":
			msg += " — " + meta.DeprecationNote
		}
		*warnings = append(*warnings, fmt.Errorf("%s", msg))
	}

	for _, group := range d.Requires {
		for _, f := range group {
			emit(f, "Requires")
		}
	}

	if acceptExpr, _ := n.Config["_port_accept_expr"].(string); acceptExpr != "" {
		if e, err := expr.Compile(acceptExpr); err == nil {
			for _, f := range e.FieldRefs() {
				emit(f, "route port expression")
			}
		}
	}

	if n.PluginName == "condition" {
		for _, ex := range conditionRuleExprs(n.Config) {
			if e, err := expr.Compile(ex); err == nil {
				for _, f := range e.FieldRefs() {
					emit(f, "condition rule")
				}
			}
		}
	}

	if n.PluginName == "require" {
		for _, f := range requireFields(n.Config) {
			emit(f, "require fields")
		}
	}

	for _, sch := range d.Schema {
		if sch.Type != plugin.FieldTypePattern {
			continue
		}
		pat, _ := n.Config[sch.Key].(string)
		if pat == "" {
			continue
		}
		ip, err := interp.Compile(pat)
		if err != nil {
			continue
		}
		for _, f := range ip.FieldRefs() {
			emit(f, fmt.Sprintf("pattern %q", sch.Key))
		}
	}
}

// conditionRuleExprs extracts all expression strings from a condition node's
// config, supporting both the single-rule form ({accept: "expr"} /
// {reject: "expr"}) and the multi-rule list form ({rules: [{accept|reject:
// "expr"}, ...]}).
func conditionRuleExprs(config map[string]any) []string {
	var out []string
	if s, ok := config["accept"].(string); ok && s != "" {
		out = append(out, s)
	}
	if s, ok := config["reject"].(string); ok && s != "" {
		out = append(out, s)
	}
	if items, ok := config["rules"].([]any); ok {
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := m["accept"].(string); ok && s != "" {
				out = append(out, s)
			}
			if s, ok := m["reject"].(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
