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
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldPublishedDate,
			entry.FieldTorrentSeeds,
			entry.FieldTorrentLeechers,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentLinkType,
			entry.FieldTorrentFileSize,
		},
		Factory:  newSourcePlugin,
		Validate: validateInput,
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
	if err := validateLimit(cfg, "jackett_input"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "timeout", "jackett_input"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "jackett_input", "url", "api_key", "indexers", "categories", "limit", "timeout", "query")...)
	return errs
}

type jackettSourcePlugin struct {
	searcher *jackettPlugin
	query    string
}

// newSourcePlugin accepts the same config as jackett plus an optional 'query'
// key (default: "", which returns recent/all results from the indexer).
func newSourcePlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	query, _ := cfg["query"].(string)

	p, err := newPlugin(cfg, db)
	if err != nil {
		return nil, err
	}

	return &jackettSourcePlugin{
		searcher: p.(*jackettPlugin),
		query:    query,
	}, nil
}

func (p *jackettSourcePlugin) Name() string        { return "jackett_input" }

func (p *jackettSourcePlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return p.searcher.Search(ctx, tc, p.query)
}
