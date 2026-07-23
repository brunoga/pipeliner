// Package library_refresh asks a Plex or Jellyfin server to rescan its
// libraries. Chain it after a download sink: chained sinks only receive
// entries the upstream sink confirmed, so the rescan fires exactly when new
// content actually landed — and at most once per run, however many entries
// arrived.
package library_refresh

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/mediaserver"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "library_refresh"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "trigger a Plex/Jellyfin library rescan after confirmed downloads (once per run)",
		Role:        plugin.RoleSink,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "backend", Type: plugin.FieldTypeString, Required: true, Hint: "Media server type: plex or jellyfin"},
			{Key: "url", Type: plugin.FieldTypeString, Required: true, Hint: "Media server base URL, e.g. http://localhost:32400"},
			{Key: "token", Type: plugin.FieldTypeString, Required: true, Hint: "Media server API token"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptUnknownKeys(cfg, pluginName, "backend", "url", "token"); err != nil {
		errs = append(errs, err...)
	}
	b, _ := cfg["backend"].(string)
	if b != "plex" && b != "jellyfin" {
		errs = append(errs, fmt.Errorf("%s: 'backend' must be plex or jellyfin", pluginName))
	}
	if u, _ := cfg["url"].(string); u == "" {
		errs = append(errs, fmt.Errorf("%s: 'url' is required", pluginName))
	}
	if t, _ := cfg["token"].(string); t == "" {
		errs = append(errs, fmt.Errorf("%s: 'token' is required", pluginName))
	}
	return errs
}

type refreshPlugin struct {
	backend string
	client  mediaserver.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	backend, _ := cfg["backend"].(string)
	url, _ := cfg["url"].(string)
	token, _ := cfg["token"].(string)
	c, err := mediaserver.New(backend, url, token)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pluginName, err)
	}
	return &refreshPlugin{backend: backend, client: c}, nil
}

func (p *refreshPlugin) Name() string { return pluginName }

// Consume implements plugin.SinkPlugin. One rescan per run, regardless of
// how many entries the upstream sink confirmed.
func (p *refreshPlugin) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	accepted := 0
	for _, e := range entries {
		if e.IsAccepted() {
			accepted++
		}
	}
	if accepted == 0 {
		return nil
	}
	if tc.DryRun {
		for _, e := range entries {
			if e.IsAccepted() {
				e.Accept(fmt.Sprintf("%s: would trigger %s rescan", pluginName, p.backend))
			}
		}
		return nil
	}
	if err := p.client.Refresh(ctx); err != nil {
		tc.Logger.Warn(pluginName+": rescan failed", "backend", p.backend, "err", err)
		// A failed rescan should not fail the downloads themselves; the
		// server will pick the files up on its own schedule.
		return nil
	}
	tc.Logger.Info(pluginName+": triggered library rescan", "backend", p.backend, "entries", accepted)
	return nil
}
