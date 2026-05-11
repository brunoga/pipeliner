// Package quality provides a filter that accepts entries matching a quality spec.
package quality

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	internalquality "github.com/brunoga/pipeliner/internal/quality"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "quality",
		Description: "reject entries whose video quality falls outside the configured range",
		PluginPhase: plugin.PhaseFilter,
		Role:        plugin.RoleProcessor,
		Requires:    []string{entry.FieldVideoQuality},
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "min", Type: plugin.FieldTypeString, Hint: "Minimum quality, e.g. 720p"},
			{Key: "max", Type: plugin.FieldTypeString, Hint: "Maximum quality, e.g. 1080p"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireOneOf(cfg, "quality", "quality", "min", "max"); err != nil {
		errs = append(errs, err)
	}
	if q, _ := cfg["min"].(string); q != "" {
		if _, err := internalquality.ParseSpec(q); err != nil {
			errs = append(errs, fmt.Errorf("quality: invalid min spec: %w", err))
		}
	}
	if q, _ := cfg["max"].(string); q != "" {
		if _, err := internalquality.ParseSpec(q); err != nil {
			errs = append(errs, fmt.Errorf("quality: invalid max spec: %w", err))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "quality", "min", "max")...)
	return errs
}

type qualityPlugin struct {
	spec internalquality.Spec
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	min, _ := cfg["min"].(string)
	max, _ := cfg["max"].(string)

	if min == "" && max == "" {
		return nil, fmt.Errorf("quality: at least one of 'min' or 'max' must be set")
	}

	// Parse min and max separately so they don't constrain each other's bound.
	// ParseSpec("720p") would set both Min and Max to 720p; we only want Min.
	var spec internalquality.Spec
	if min != "" {
		s, err := internalquality.ParseSpec(min)
		if err != nil {
			return nil, fmt.Errorf("quality: invalid min spec: %w", err)
		}
		spec.MinResolution = s.MinResolution
		spec.MinSource = s.MinSource
		spec.MinCodec = s.MinCodec
		spec.MinAudio = s.MinAudio
		spec.MinColorRange = s.MinColorRange
	}
	if max != "" {
		s, err := internalquality.ParseSpec(max)
		if err != nil {
			return nil, fmt.Errorf("quality: invalid max spec: %w", err)
		}
		spec.MaxResolution = s.MaxResolution
		spec.MaxSource = s.MaxSource
		spec.MaxCodec = s.MaxCodec
		spec.MaxAudio = s.MaxAudio
		spec.MaxColorRange = s.MaxColorRange
	}
	return &qualityPlugin{spec: spec}, nil
}

func (p *qualityPlugin) Name() string        { return "quality" }
func (p *qualityPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *qualityPlugin) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	q := internalquality.Parse(e.Title)
	e.Set("quality", q.String())

	if !p.spec.Matches(q) {
		e.Reject(fmt.Sprintf("quality %s does not match spec", q.String()))
	}
	return nil
}

func (p *qualityPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.Filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}
