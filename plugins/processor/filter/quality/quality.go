// Package quality provides a filter processor that rejects entries whose
// parsed quality does not match a configured quality spec.
//
// Spec syntax (see internal/quality.ParseSpec for the full grammar):
//
//	720p        - exact resolution
//	720p+       - 720p or better
//	720p-1080p  - inclusive range
//	web         - source-only spec
//	720p+ web   - combined dimensions
//
// Config keys:
//
//	spec       - required; the quality spec entries must match
//	on_missing - "pass" (default) or "reject" when an entry has no _quality
package quality

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "quality",
		Description: "reject entries whose parsed quality does not match the configured spec",
		Role:        plugin.RoleProcessor,
		Requires:    plugin.RequireAll(entry.FieldQuality),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "spec", Type: plugin.FieldTypeString, Required: true, Hint: "Quality spec, e.g. 720p+ (floor), 720p (exact), 720p-1080p (range)"},
			{Key: "on_missing", Type: plugin.FieldTypeEnum, Enum: []string{"pass", "reject"}, Default: "pass", Hint: "Behaviour when _quality is absent on an entry (default: pass)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	specStr, _ := cfg["spec"].(string)
	if specStr == "" {
		errs = append(errs, fmt.Errorf("quality: %q is required", "spec"))
	} else if _, err := quality.ParseSpec(specStr); err != nil {
		errs = append(errs, fmt.Errorf("quality: invalid spec: %w", err))
	}
	if err := plugin.OptEnum(cfg, "on_missing", "quality", "pass", "reject"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "quality", "spec", "on_missing")...)
	return errs
}

type qualityPlugin struct {
	spec      quality.Spec
	specStr   string // original config string, used in reject reasons
	onMissing string // "pass" or "reject"
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	specStr, _ := cfg["spec"].(string)
	if specStr == "" {
		return nil, fmt.Errorf("quality: %q is required", "spec")
	}
	spec, err := quality.ParseSpec(specStr)
	if err != nil {
		return nil, fmt.Errorf("quality: invalid spec: %w", err)
	}
	onMissing, _ := cfg["on_missing"].(string)
	if onMissing == "" {
		onMissing = "pass"
	}
	return &qualityPlugin{spec: spec, specStr: specStr, onMissing: onMissing}, nil
}

func (p *qualityPlugin) Name() string { return "quality" }

func (p *qualityPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		q, ok := e.Quality()
		if !ok {
			if p.onMissing == "reject" {
				e.Reject("quality: entry has no _quality")
			}
			continue
		}
		if !p.spec.Matches(q) {
			e.Reject(fmt.Sprintf("quality: %s does not match spec %q", q.String(), p.specStr))
		}
	}
	return entry.PassThrough(entries), nil
}
