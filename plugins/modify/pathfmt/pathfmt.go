// Package pathfmt sets download_path on each entry by rendering a path pattern.
//
// Patterns use {field} or {field:format} syntax. Go template syntax ({{.field}})
// is also accepted for backward compatibility.
package pathfmt

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
		PluginName:  "pathfmt",
		Description: "set download_path from a pattern rendered against entry fields",
		PluginPhase: plugin.PhaseModify,
		Factory:     newPlugin,
	})
}

type pathfmtPlugin struct {
	ip *interp.Interpolator
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	path, _ := cfg["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("pathfmt: 'path' is required")
	}
	ip, err := interp.Compile(path)
	if err != nil {
		return nil, fmt.Errorf("pathfmt: invalid path pattern: %w", err)
	}
	return &pathfmtPlugin{ip: ip}, nil
}

func (p *pathfmtPlugin) Name() string        { return "pathfmt" }
func (p *pathfmtPlugin) Phase() plugin.Phase { return plugin.PhaseModify }

func (p *pathfmtPlugin) Modify(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	result, err := p.ip.Render(interp.EntryData(e))
	if err != nil {
		return fmt.Errorf("pathfmt: render: %w", err)
	}
	e.Set("download_path", result)
	return nil
}
