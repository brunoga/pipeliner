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
		Factory:     newPlugin,
		Validate:    validate,
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
	rules []rule
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
	return p, nil
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

func (p *conditionPlugin) Name() string        { return "condition" }

func (p *conditionPlugin) filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	data := interp.EntryData(e)
	for _, r := range p.rules {
		// Within a rule, reject wins over accept.
		if r.reject != nil {
			matched, err := r.reject.Eval(data)
			if err != nil {
				return fmt.Errorf("condition: reject expression: %w", err)
			}
			if matched {
				e.Reject(fmt.Sprintf("condition: %s", r.rejectExpr))
				return nil
			}
		}
		if r.accept != nil {
			matched, err := r.accept.Eval(data)
			if err != nil {
				return fmt.Errorf("condition: accept expression: %w", err)
			}
			if matched {
				e.Accept()
				return nil
			}
		}
	}
	return nil
}

func (p *conditionPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}
