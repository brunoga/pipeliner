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
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "set",
		Description: "set entry fields; values are patterns interpolated against entry fields",
		PluginPhase: plugin.PhaseModify,
		Factory:     newSetPlugin,
	})
}

type setPlugin struct {
	fields map[string]*interp.Interpolator
}

func newSetPlugin(cfg map[string]any) (plugin.Plugin, error) {
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
