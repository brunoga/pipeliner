// Package condition provides a conditional filter plugin that accepts or rejects
// entries based on boolean expressions evaluated against entry fields.
//
// Expressions use infix syntax: field > value, field == "string", etc.
// Go template syntax ({{gt .field value}}) is also accepted for backward compat.
// Reject wins over accept within the same rule. Rules are evaluated in order;
// the first rule whose condition fires terminates processing.
//
// Two config formats are supported:
//
// Single-rule:
//
//	condition:
//	  reject: 'source == "CAM"'
//	  accept: 'tmdb_vote_average >= 7.0'
//
// Multi-rule (avoids YAML duplicate-key problem):
//
//	condition:
//	  rules:
//	    - reject: 'source == "CAM"'
//	    - reject: 'tmdb_vote_average < 6.5'
//	    - accept: 'true'
package condition

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/expr"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "condition",
		Description: "accept or reject entries via boolean expressions",
		Role:        plugin.RoleProcessor,
		// Accept markers so users can write `accept: "empty_marker == true"`
		// to gate marker-aware branches. The expression evaluator already
		// reads e.Fields, so the empty_marker field is visible without any
		// further plumbing.
		AcceptsMarkers: true,
		Factory:        newPlugin,
		Validate:       validate,
		Schema: []plugin.FieldSchema{
			{Key: "accept", Type: plugin.FieldTypeString, Hint: "Expression; entry accepted when true"},
			{Key: "reject", Type: plugin.FieldTypeString, Hint: "Expression; entry rejected when true"},
			{Key: "rules", Type: plugin.FieldTypeList, Hint: "Ordered list of {accept, reject} rule objects"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireOneOf(cfg, "condition", "rules", "accept", "reject"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "condition", "rules", "accept", "reject")...)
	return errs
}

type rule struct {
	accept     *expr.Expr
	reject     *expr.Expr
	acceptExpr string
	rejectExpr string
}

type conditionPlugin struct {
	rules         []rule
	referencesSt  bool // any rule expression references the `state` identifier
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	p := &conditionPlugin{}

	if rulesRaw, ok := cfg["rules"]; ok {
		list, ok := rulesRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("condition: 'rules' must be a list of {accept/reject} objects")
		}
		for i, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("condition: rules[%d] must be a map with accept/reject keys", i)
			}
			r, err := parseRule(m, fmt.Sprintf("rules[%d]", i))
			if err != nil {
				return nil, err
			}
			p.rules = append(p.rules, r)
		}
		if len(p.rules) == 0 {
			return nil, fmt.Errorf("condition: 'rules' must not be empty")
		}
		p.computeStateReferences()
		return p, nil
	}

	// Single-rule format: top-level accept/reject keys.
	r, err := parseRule(cfg, "")
	if err != nil {
		return nil, err
	}
	if r.accept == nil && r.reject == nil {
		return nil, fmt.Errorf("condition: at least one of 'accept', 'reject', or 'rules' must be set")
	}
	p.rules = []rule{r}
	p.computeStateReferences()
	return p, nil
}

// computeStateReferences flips referencesSt when any of the plugin's
// expressions read the reserved `state` identifier. Used by
// EffectiveInputStates below to widen the executor's pre-filter so the
// expressions actually see the entries they were written for.
func (p *conditionPlugin) computeStateReferences() {
	for _, r := range p.rules {
		if r.accept != nil && r.accept.ReferencesState() {
			p.referencesSt = true
			return
		}
		if r.reject != nil && r.reject.ReferencesState() {
			p.referencesSt = true
			return
		}
	}
}

// EffectiveInputStates widens the executor's state pre-filter to all four
// states when any rule expression references `state`. Without this, an
// entry the user wrote a rule for (say `accept: state == "failed"`) would
// be hidden by the default StatesAcceptedUndecided pre-filter and the rule
// would never fire. When no rule references state we keep the default so
// existing configurations behave unchanged.
func (p *conditionPlugin) EffectiveInputStates() entry.StateSet {
	if p.referencesSt {
		return entry.StatesAll
	}
	return entry.StatesAcceptedUndecided
}

func parseRule(m map[string]any, prefix string) (rule, error) {
	label := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + " " + k
	}
	var r rule
	if a, _ := m["accept"].(string); a != "" {
		e, err := expr.Compile(a)
		if err != nil {
			return rule{}, fmt.Errorf("condition: invalid %s expression: %w", label("accept"), err)
		}
		r.accept = e
		r.acceptExpr = a
	}
	if rej, _ := m["reject"].(string); rej != "" {
		e, err := expr.Compile(rej)
		if err != nil {
			return rule{}, fmt.Errorf("condition: invalid %s expression: %w", label("reject"), err)
		}
		r.reject = e
		r.rejectExpr = rej
	}
	return r, nil
}

func (p *conditionPlugin) Name() string { return "condition" }

func (p *conditionPlugin) filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	// EntryDataWithState exposes the entry's state as the `state` identifier
	// (and "State" for backwards-compat with {{.State}} templates) so rule
	// expressions can branch on it. Reject reason is exposed too for
	// expressions like `reject_reason contains "quota"`.
	data := interp.EntryDataWithState(e)
	for _, r := range p.rules {
		// Within a rule, reject wins over accept.
		if r.reject != nil {
			matched, err := r.reject.Eval(data)
			if err != nil {
				return fmt.Errorf("condition: reject expression: %w", err)
			}
			if matched {
				// Reject is unconditional in the entry state machine, but
				// transitioning Failed → Rejected would lose the original
				// failure reason. Leave Failed entries alone; the user
				// almost certainly didn't mean to overwrite Failed with
				// Rejected via a condition rule.
				if !e.IsFailed() {
					e.Reject(fmt.Sprintf("condition: %s", r.rejectExpr))
				}
				return nil
			}
		}
		if r.accept != nil {
			matched, err := r.accept.Eval(data)
			if err != nil {
				return fmt.Errorf("condition: accept expression: %w", err)
			}
			if matched {
				// Accept() naively un-Fails Failed entries (the state machine
				// only no-ops for Rejected). When `state` widening puts a
				// Failed entry in front of an accept rule, treat the match as
				// a port-style decision: leave state as-is. This preserves
				// the rule's observation without lying about the entry's
				// terminal status.
				if !e.IsFailed() {
					e.Accept()
				}
				return nil
			}
		}
	}
	return nil
}

func (p *conditionPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}
