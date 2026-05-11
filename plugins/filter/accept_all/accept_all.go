// Package accept_all provides a filter plugin that accepts every undecided entry.
package accept_all

import (
	"context"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "accept_all",
		Description: "accept every undecided entry unconditionally",
		PluginPhase: plugin.PhaseFilter,
		Role:        plugin.RoleProcessor,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &acceptAllPlugin{}, nil
		},
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "accept_all")
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

func (p *acceptAllPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
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
