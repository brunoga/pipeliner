package config

import (
	"fmt"
	"strings"
)

// ParseAfter splits an after= spec into its parent pipeline name and
// condition ("" = always, "accepted" = only when the parent accepted ≥1).
func ParseAfter(spec string) (parent, condition string, err error) {
	parent, condition, _ = strings.Cut(spec, ":")
	if parent == "" {
		return "", "", fmt.Errorf("after: missing pipeline name in %q", spec)
	}
	if condition != "" && condition != "accepted" {
		return "", "", fmt.Errorf("after: unknown condition %q (supported: accepted)", condition)
	}
	return parent, condition, nil
}

// validateAfter checks every after= reference: the parent must exist, no
// pipeline may follow itself, and the dependency graph must be acyclic
// (a cycle would trigger forever).
func validateAfter(c *Config) []error {
	var errs []error
	for name, spec := range c.GraphAfter {
		parent, _, err := ParseAfter(spec)
		if err != nil {
			errs = append(errs, fmt.Errorf("pipeline %q: %w", name, err))
			continue
		}
		if parent == name {
			errs = append(errs, fmt.Errorf("pipeline %q: after must not reference itself", name))
			continue
		}
		if _, ok := c.Graphs[parent]; !ok {
			errs = append(errs, fmt.Errorf("pipeline %q: after references unknown pipeline %q", name, parent))
		}
	}
	// Cycle walk: follow parents; a repeat within one walk is a cycle.
	for name := range c.GraphAfter {
		seen := map[string]bool{name: true}
		cur := name
		for {
			spec, ok := c.GraphAfter[cur]
			if !ok {
				break
			}
			parent, _, err := ParseAfter(spec)
			if err != nil || parent == cur {
				break // malformed or self-reference: already reported above
			}
			if seen[parent] {
				errs = append(errs, fmt.Errorf("pipeline %q: after chain forms a cycle through %q", name, parent))
				break
			}
			seen[parent] = true
			cur = parent
		}
	}
	return errs
}

// Dependents returns the pipelines to fire after parent completed a real
// (non-dry) run that accepted acceptedCount entries.
func Dependents(after map[string]string, parent string, acceptedCount int) []string {
	var out []string
	for name, spec := range after {
		p, cond, err := ParseAfter(spec)
		if err != nil || p != parent {
			continue
		}
		if cond == "accepted" && acceptedCount == 0 {
			continue
		}
		out = append(out, name)
	}
	return out
}
