// Package accept_all provides a filter plugin that accepts every undecided entry.
package accept_all

import (
	"context"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "accept_all",
		Description: "accept every undecided entry unconditionally",
		PluginPhase: plugin.PhaseFilter,
		Factory: func(cfg map[string]any) (plugin.Plugin, error) {
			return &acceptAllPlugin{}, nil
		},
	})
}

type acceptAllPlugin struct{}

func (p *acceptAllPlugin) Name() string        { return "accept_all" }
func (p *acceptAllPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }
func (p *acceptAllPlugin) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	if !e.IsAccepted() && !e.IsRejected() {
		e.Accept()
	}
	return nil
}
