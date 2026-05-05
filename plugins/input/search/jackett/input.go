package jackett

import (
	"context"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "jackett_input",
		Description: "return recent results from Jackett indexers as pipeline entries (no query required)",
		PluginPhase: plugin.PhaseInput,
		Factory:     newInputPlugin,
		Validate:    validateInput,
	})
}

func validateInput(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "url", "jackett_input"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "api_key", "jackett_input"); err != nil {
		errs = append(errs, err)
	}
	return errs
}

type jackettInputPlugin struct {
	searcher *jackettPlugin
	query    string
}

// newInputPlugin accepts the same config as jackett plus an optional 'query'
// key (default: "", which returns recent/all results from the indexer).
func newInputPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	query, _ := cfg["query"].(string)

	p, err := newPlugin(cfg, db)
	if err != nil {
		return nil, err
	}

	return &jackettInputPlugin{
		searcher: p.(*jackettPlugin),
		query:    query,
	}, nil
}

func (p *jackettInputPlugin) Name() string        { return "jackett_input" }
func (p *jackettInputPlugin) Phase() plugin.Phase { return plugin.PhaseInput }

func (p *jackettInputPlugin) Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return p.searcher.Search(ctx, tc, p.query)
}
