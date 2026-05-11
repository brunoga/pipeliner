// Package set provides a modify plugin that sets entry fields from patterns.
//
// Values use {field} or {field:format} syntax. Go template syntax ({{.field}})
// is also accepted for backward compatibility.
package set

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "set",
		Description: "set entry fields; values are patterns interpolated against entry fields",
		PluginPhase: plugin.PhaseModify,
		Role:        plugin.RoleProcessor,
		Factory:     newSetPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	if len(cfg) == 0 {
		return []error{fmt.Errorf("set: at least one field must be configured")}
	}
	return nil
}

type setPlugin struct {
	fields map[string]*interp.Interpolator
}

func newSetPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	fields := make(map[string]*interp.Interpolator, len(cfg))
	for k, v := range cfg {
		s := fmt.Sprintf("%v", v)
		ip, err := interp.Compile(s)
		if err != nil {
			return nil, fmt.Errorf("set: field %q: invalid pattern %q: %w", k, s, err)
		}
		fields[k] = ip
	}
	return &setPlugin{fields: fields}, nil
}

func (s *setPlugin) Name() string        { return "set" }
func (s *setPlugin) Phase() plugin.Phase { return plugin.PhaseModify }

func (s *setPlugin) Modify(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	data := interp.EntryData(e)
	for key, ip := range s.fields {
		val, err := ip.Render(data)
		if err != nil {
			return fmt.Errorf("set: field %q: render: %w", key, err)
		}
		e.Set(key, val)
	}
	return nil
}

func (s *setPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := s.Modify(ctx, tc, e); err != nil {
			tc.Logger.Warn("set error", "entry", e.Title, "err", err)
		}
	}
	return entries, nil
}
