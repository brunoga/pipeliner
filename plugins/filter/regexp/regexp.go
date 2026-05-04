// Package regexp provides a filter plugin that accepts or rejects entries
// based on regular expression patterns matched against entry fields.
package regexp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "regexp",
		Description: "accept or reject entries by matching regular expressions against fields",
		PluginPhase: plugin.PhaseFilter,
		Factory:     newRegexpPlugin,
	})
}

// regexpRule pairs a compiled regexp with an optional per-pattern field list.
// When from is nil, the plugin's global from list is used.
type regexpRule struct {
	re   *regexp.Regexp
	from []string // nil means use the plugin's global from
}

type regexpPlugin struct {
	accept []regexpRule
	reject []regexpRule
	from   []string // entry fields to match against; defaults to ["title"]
}

func newRegexpPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	p := &regexpPlugin{from: []string{"title"}}

	if err := p.compilePatterns(cfg, "accept", &p.accept); err != nil {
		return nil, err
	}
	if err := p.compilePatterns(cfg, "reject", &p.reject); err != nil {
		return nil, err
	}

	if raw, ok := cfg["from"]; ok {
		fields, err := toStringSlice(raw)
		if err != nil {
			return nil, fmt.Errorf("regexp: 'from' must be a string or list of strings: %w", err)
		}
		p.from = fields
	}

	return p, nil
}

func (p *regexpPlugin) compilePatterns(cfg map[string]any, key string, dest *[]regexpRule) error {
	raw, ok := cfg[key]
	if !ok {
		return nil
	}

	// The value may be a string, []string, or []any (may contain strings or maps).
	items, err := toAnySlice(raw)
	if err != nil {
		return fmt.Errorf("regexp: %q must be a string or list: %w", key, err)
	}

	for _, item := range items {
		rule, err := parseRule(item)
		if err != nil {
			return fmt.Errorf("regexp: %q entry: %w", key, err)
		}
		*dest = append(*dest, rule)
	}
	return nil
}

// parseRule parses a single accept/reject list entry. It accepts:
//   - plain string: uses global from
//   - map with "pattern" key and optional "from" key: uses per-pattern from
func parseRule(item any) (regexpRule, error) {
	switch v := item.(type) {
	case string:
		re, err := regexp.Compile(v)
		if err != nil {
			return regexpRule{}, fmt.Errorf("invalid pattern %q: %w", v, err)
		}
		return regexpRule{re: re}, nil

	case map[string]any:
		patRaw, ok := v["pattern"]
		if !ok {
			return regexpRule{}, fmt.Errorf("map entry missing 'pattern' key")
		}
		pat, ok := patRaw.(string)
		if !ok {
			return regexpRule{}, fmt.Errorf("'pattern' must be a string, got %T", patRaw)
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return regexpRule{}, fmt.Errorf("invalid pattern %q: %w", pat, err)
		}
		var from []string
		if fromRaw, ok := v["from"]; ok {
			from, err = toStringSlice(fromRaw)
			if err != nil {
				return regexpRule{}, fmt.Errorf("'from' must be a string or list of strings: %w", err)
			}
		}
		return regexpRule{re: re, from: from}, nil

	default:
		return regexpRule{}, fmt.Errorf("unsupported type %T", item)
	}
}

func (p *regexpPlugin) Name() string        { return "regexp" }
func (p *regexpPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *regexpPlugin) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	// Reject takes priority: check reject patterns first.
	for _, rule := range p.reject {
		from := rule.from
		if from == nil {
			from = p.from
		}
		if matchesAnyField(rule.re, from, e) {
			e.Reject(fmt.Sprintf("regexp reject: %s", rule.re))
			return nil
		}
	}
	// If any accept pattern matches, accept the entry.
	if len(p.accept) > 0 {
		for _, rule := range p.accept {
			from := rule.from
			if from == nil {
				from = p.from
			}
			if matchesAnyField(rule.re, from, e) {
				e.Accept()
				return nil
			}
		}
		// Accept list defined but nothing matched — leave undecided so
		// subsequent filters can still make a decision.
	}
	return nil
}

func matchesAnyField(re *regexp.Regexp, from []string, e *entry.Entry) bool {
	for _, field := range from {
		var val string
		switch field {
		case "title":
			val = e.Title
		case "url":
			val = e.URL
		default:
			val = e.GetString(field)
		}
		if re.MatchString(val) {
			return true
		}
	}
	return false
}

// toAnySlice coerces a config value to []any. It accepts a bare string,
// a bare map, a []any, or a []string.
func toAnySlice(v any) ([]any, error) {
	switch t := v.(type) {
	case string:
		return []any{t}, nil
	case map[string]any:
		return []any{t}, nil
	case []any:
		return t, nil
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, nil
	case json.RawMessage:
		// Should not happen after config parsing, but handle gracefully.
		var s string
		if err := json.Unmarshal(t, &s); err == nil {
			return []any{s}, nil
		}
		var arr []any
		if err := json.Unmarshal(t, &arr); err == nil {
			return arr, nil
		}
		return nil, fmt.Errorf("cannot parse JSON as string or list")
	}
	return nil, fmt.Errorf("unsupported type %T", v)
}

// toStringSlice coerces a config value to []string. It accepts a bare string,
// a []string, or a []any whose elements are strings (as produced by JSON decode).
func toStringSlice(v any) ([]string, error) {
	switch t := v.(type) {
	case string:
		return []string{t}, nil
	case []string:
		return t, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("list element is not a string: %T", item)
			}
			out = append(out, s)
		}
		return out, nil
	case json.RawMessage:
		// Should not happen after config parsing, but handle gracefully.
		var s string
		if err := json.Unmarshal(t, &s); err == nil {
			return []string{s}, nil
		}
		var ss []string
		if err := json.Unmarshal(t, &ss); err == nil {
			return ss, nil
		}
		return nil, fmt.Errorf("cannot parse JSON as string or []string")
	}
	return nil, fmt.Errorf("unsupported type %T", v)
}
