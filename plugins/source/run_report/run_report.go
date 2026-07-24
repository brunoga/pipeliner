// Package run_report emits one entry per traced pipeline run within the
// window — the "what happened and why" feed. Each entry carries the run's
// accepted/rejected/failed counts plus the most common rejection reasons, so
// a notify digest downstream becomes a weekly activity report. Entries have
// stable pipeliner://run/… URLs: put a URL-keyed seen filter downstream to
// report each run exactly once.
package run_report

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/traces"
)

const pluginName = "run_report"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "emit one entry per traced pipeline run (within window) with counts and top rejection reasons",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTitle, "report_task", "report_run_id",
			"report_accepted", "report_rejected", "report_failed",
		},
		MayProduce: []string{"report_top_rejects"},
		Factory:    newPlugin,
		Validate:   validate,
		Schema: []plugin.FieldSchema{
			{Key: "window", Type: plugin.FieldTypeDuration, Default: "168h", Hint: "How far back to report"},
			{Key: "tasks", Type: plugin.FieldTypeList, Hint: "Pipelines to report on (default: every pipeline with traces)"},
			{Key: "include_dry", Type: plugin.FieldTypeBool, Hint: "Include dry-runs in the report"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptUnknownKeys(cfg, pluginName, "window", "tasks", "include_dry"); err != nil {
		errs = append(errs, err...)
	}
	if err := plugin.OptDuration(cfg, "window", pluginName); err != nil {
		errs = append(errs, err)
	}
	return errs
}

type reportPlugin struct {
	store      *traces.Store
	window     time.Duration
	tasks      []string
	includeDry bool
	now        func() time.Time
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	if db == nil {
		return nil, fmt.Errorf("%s: requires the store", pluginName)
	}
	window := 168 * time.Hour
	if s, ok := cfg["window"].(string); ok && s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid window: %w", pluginName, err)
		}
		window = d
	}
	var tasks []string
	if raw, ok := cfg["tasks"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				tasks = append(tasks, s)
			}
		}
	}
	includeDry, _ := cfg["include_dry"].(bool)
	return &reportPlugin{
		store:      traces.NewStore(db.Bucket(traces.BucketName)),
		window:     window,
		tasks:      tasks,
		includeDry: includeDry,
		now:        time.Now,
	}, nil
}

func (p *reportPlugin) Name() string { return pluginName }

// summarize computes counts and the top rejection reasons for one run.
func summarize(rt *traces.RunTrace) (accepted, rejected, failed int, topRejects string) {
	reasons := map[string]int{}
	for _, et := range rt.Entries {
		switch et.Final {
		case "accepted", "consumed":
			accepted++
		case "rejected":
			rejected++
			if et.Reason != "" {
				reasons[et.Reason]++
			}
		case "failed":
			failed++
		}
	}
	type rc struct {
		reason string
		n      int
	}
	var top []rc
	for r, n := range reasons {
		top = append(top, rc{r, n})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].n != top[j].n {
			return top[i].n > top[j].n
		}
		return top[i].reason < top[j].reason
	})
	if len(top) > 3 {
		top = top[:3]
	}
	parts := make([]string, len(top))
	for i, t := range top {
		parts[i] = fmt.Sprintf("%s (%d)", t.reason, t.n)
	}
	return accepted, rejected, failed, strings.Join(parts, "; ")
}

// Generate implements plugin.SourcePlugin.
func (p *reportPlugin) Generate(_ context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	tasks := p.tasks
	if len(tasks) == 0 {
		var err error
		tasks, err = p.store.Tasks()
		if err != nil {
			return nil, fmt.Errorf("%s: list tasks: %w", pluginName, err)
		}
	}
	cutoff := p.now().Add(-p.window)

	var out []*entry.Entry
	for _, task := range tasks {
		metas, err := p.store.List(task)
		if err != nil {
			tc.Logger.Warn(pluginName+": list runs", "task", task, "err", err)
			continue
		}
		for _, m := range metas {
			if m.At.Before(cutoff) || (m.DryRun && !p.includeDry) {
				continue
			}
			rt, err := p.store.Get(task, m.RunID)
			if err != nil {
				tc.Logger.Warn(pluginName+": load trace", "task", task, "run", m.RunID, "err", err)
				continue
			}
			acc, rej, fail, top := summarize(rt)
			dry := ""
			if rt.DryRun {
				dry = " (dry)"
			}
			e := entry.New(
				fmt.Sprintf("%s%s: %d accepted, %d rejected, %d failed", task, dry, acc, rej, fail),
				fmt.Sprintf("pipeliner://run/%s/%s", task, m.RunID),
			)
			e.Fields["report_task"] = task
			e.Fields["report_run_id"] = m.RunID
			e.Fields["report_accepted"] = acc
			e.Fields["report_rejected"] = rej
			e.Fields["report_failed"] = fail
			if top != "" {
				e.Fields["report_top_rejects"] = top
			}
			out = append(out, e)
		}
	}
	tc.Logger.Info(pluginName+": runs reported", "count", len(out), "window", p.window.String())
	return out, nil
}
