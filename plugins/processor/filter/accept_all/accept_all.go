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
		Role:        plugin.RoleProcessor,
		// Only act on Undecided entries — never re-decide entries that have
		// already been accepted, rejected, or failed by an upstream node.
		InputStates: entry.StatesUndecidedOnly,
		Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
			return &acceptAllPlugin{}, nil
		},
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "accept_all")
		},
	})
}

type acceptAllPlugin struct{}

func (p *acceptAllPlugin) Name() string { return "accept_all" }
func (p *acceptAllPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	// Executor pre-filter (InputStates=StatesUndecidedOnly) means every entry
	// here is Undecided — no per-entry state check needed.
	for _, e := range entries {
		e.Accept()
	}
	return entry.PassThrough(entries), nil
}
