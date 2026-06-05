package dag

import (
	"fmt"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// conditionMissingRejectWarning emits an advisory warning when a `condition`
// node has no reject branch at all. Such a node leaves every non-matching
// entry in state Undecided, which PassThrough forwards as-is — so the filter
// silently does nothing for the vast majority of entries that the user almost
// certainly intended to drop. The two correct patterns are:
//
//	reject = "not (X)"                                 # express the exclusion
//	rules  = [{"accept": "X"}, {"reject": "true"}]     # accept + catch-all
//
// Returns nil for non-condition plugins or for condition nodes that already
// have at least one reject expression somewhere.
func conditionMissingRejectWarning(n *Node, d *plugin.Descriptor) error {
	if d == nil || d.PluginName != "condition" {
		return nil
	}
	if conditionHasReject(n.Config) {
		return nil
	}
	return fmt.Errorf("node %q (plugin %q): no reject expression — accept-only condition leaves non-matching entries Undecided, which then pass through to the next node unchanged. Use reject=\"not (X)\" or add a catch-all {\"reject\": \"true\"} rule to filter exclusively", n.ID, n.PluginName)
}

// conditionHasReject reports whether a condition node's config carries any
// reject expression — top-level `reject` in the single-rule form, or any
// item with a `reject` key inside the `rules` list. Mirrors the precedence
// rule in plugins/processor/filter/condition/condition.go: when `rules` is
// present, the top-level accept/reject keys are ignored.
func conditionHasReject(cfg map[string]any) bool {
	if cfg == nil {
		return false
	}
	if rulesRaw, ok := cfg["rules"]; ok {
		list, _ := rulesRaw.([]any)
		for _, item := range list {
			m, _ := item.(map[string]any)
			if rej, _ := m["reject"].(string); rej != "" {
				return true
			}
		}
		return false
	}
	if rej, _ := cfg["reject"].(string); rej != "" {
		return true
	}
	return false
}
