// Package webhook turns pushed HTTP payloads into pipeline entries. The web
// server's POST /api/ingest/{queue} endpoint (enabled by PIPELINER_INGEST_TOKEN)
// queues items; this source drains its configured queue on each run. Pair the
// endpoint's ?pipeline= parameter with this pipeline's name so a push triggers
// the run immediately instead of waiting for the schedule.
package webhook

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/ingest"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "webhook"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "emit entries pushed to POST /api/ingest/{queue} (autobrr, IRC bridges, anything that can POST JSON)",
		Role:        plugin.RoleSource,
		Produces:    []string{entry.FieldTitle},
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "queue", Type: plugin.FieldTypeString, Required: true, Hint: "Ingest queue name — the {queue} path segment pushers POST to"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptUnknownKeys(cfg, pluginName, "queue"); err != nil {
		errs = append(errs, err...)
	}
	if q, _ := cfg["queue"].(string); q == "" {
		errs = append(errs, fmt.Errorf("%s: 'queue' is required", pluginName))
	}
	return errs
}

type webhookPlugin struct {
	queue string
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	q, _ := cfg["queue"].(string)
	if q == "" {
		return nil, fmt.Errorf("%s: 'queue' is required", pluginName)
	}
	return &webhookPlugin{queue: q}, nil
}

func (p *webhookPlugin) Name() string { return pluginName }

// Generate implements plugin.SourcePlugin: drain everything pushed since the
// last run. Items without a URL get a synthetic one so dedup still works.
func (p *webhookPlugin) Generate(_ context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	items := ingest.Drain(p.queue)
	out := make([]*entry.Entry, 0, len(items))
	for i, it := range items {
		if it.Title == "" {
			continue // enforced at the endpoint; belt and braces here
		}
		u := it.URL
		if u == "" {
			u = fmt.Sprintf("pipeliner://webhook/%s/%d/%s", p.queue, i, it.Title)
		}
		e := entry.New(it.Title, u)
		for k, v := range it.Fields {
			e.Fields[k] = v
		}
		out = append(out, e)
	}
	if len(out) > 0 {
		tc.Logger.Info(pluginName+": drained pushed items", "queue", p.queue, "count", len(out))
	}
	return out, nil
}
