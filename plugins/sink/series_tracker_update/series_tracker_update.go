// Package series_tracker_update provides a sink that flips a tracked show's
// inactive flag in the series tracker. Deactivated shows are rejected early
// by the series filter ("series: <show> inactive (<reason>)"), so pipelines
// stop searching for them; reactivating removes the flag.
//
// Typical use: chain it after a series_lifecycle "complete" classification —
// see configs/series-lifecycle.star. The flag lives in a store bucket shared
// across all tasks (series.InactiveBucketName), so a deactivation performed
// by one pipeline applies to every pipeline's series filter.
//
// Config keys:
//
//	action - "deactivate" or "reactivate" (required)
//	reason - reason recorded on deactivation and echoed in the series
//	         filter's rejection message (default: the entry's
//	         series_lifecycle value, falling back to "deactivated")
package series_tracker_update

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "series_tracker_update"

const (
	actionDeactivate = "deactivate"
	actionReactivate = "reactivate"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "deactivate or reactivate a tracked show in the series tracker (inactive shows are rejected by the series filter)",
		Role:        plugin.RoleSink,
		Requires:    plugin.RequireAll(entry.FieldSeriesName),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "action", Type: plugin.FieldTypeEnum, Enum: []string{actionDeactivate, actionReactivate}, Required: true, Hint: "deactivate rejects the show's episodes in the series filter; reactivate restores it"},
			{Key: "reason", Type: plugin.FieldTypeString, Hint: "Reason recorded on deactivation (default: the entry's series_lifecycle value, else \"deactivated\")"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "action", pluginName); err != nil {
		errs = append(errs, err)
	} else if err := plugin.OptEnum(cfg, "action", pluginName, actionDeactivate, actionReactivate); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, "action", "reason")...)
	return errs
}

type trackerUpdateSink struct {
	inactive *series.InactiveSet
	action   string
	reason   string
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	action, _ := cfg["action"].(string)
	if action != actionDeactivate && action != actionReactivate {
		return nil, fmt.Errorf("%s: action must be %q or %q, got %q",
			pluginName, actionDeactivate, actionReactivate, action)
	}
	reason, _ := cfg["reason"].(string)
	return &trackerUpdateSink{
		inactive: series.NewInactiveSet(db.Bucket(series.InactiveBucketName)),
		action:   action,
		reason:   reason,
	}, nil
}

func (p *trackerUpdateSink) Name() string { return pluginName }

// reasonFor picks the deactivation reason: explicit config wins, then the
// entry's series_lifecycle value (so the series filter says
// "inactive (complete)"), then a generic fallback.
func (p *trackerUpdateSink) reasonFor(e *entry.Entry) string {
	if p.reason != "" {
		return p.reason
	}
	if lc := e.GetString(entry.FieldSeriesLifecycle); lc != "" {
		return lc
	}
	return "deactivated"
}

func (p *trackerUpdateSink) Consume(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		name := e.GetString(entry.FieldSeriesName)
		if name == "" {
			e.Fail(pluginName + ": entry has no series_name")
			continue
		}

		if tc.DryRun {
			switch p.action {
			case actionDeactivate:
				e.Accept(fmt.Sprintf("%s: would deactivate %s (%s)", pluginName, name, p.reasonFor(e)))
			case actionReactivate:
				e.Accept(fmt.Sprintf("%s: would reactivate %s", pluginName, name))
			}
			tc.Logger.Info(pluginName+": dry-run", "action", p.action, "series", name)
			continue
		}

		switch p.action {
		case actionDeactivate:
			reason := p.reasonFor(e)
			if err := p.inactive.Deactivate(name, reason); err != nil {
				e.Fail(fmt.Sprintf("%s: deactivate %s: %v", pluginName, name, err))
				continue
			}
			e.Accept(fmt.Sprintf("%s: deactivated %s (%s)", pluginName, name, reason))
			tc.Logger.Info(pluginName+": deactivated", "series", name, "reason", reason)
		case actionReactivate:
			if err := p.inactive.Reactivate(name); err != nil {
				e.Fail(fmt.Sprintf("%s: reactivate %s: %v", pluginName, name, err))
				continue
			}
			e.Accept(fmt.Sprintf("%s: reactivated %s", pluginName, name))
			tc.Logger.Info(pluginName+": reactivated", "series", name)
		}
	}
	return nil
}
