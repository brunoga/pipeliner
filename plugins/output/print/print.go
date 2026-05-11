// Package print provides a simple stdout output plugin.
//
// The format string uses {field} or {field:format} syntax. Go template syntax
// ({{.field}}) is also accepted for backward compatibility.
package print

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const defaultFormat = "{title}\t{url}"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "print",
		Description: "print accepted entries to stdout",
		PluginPhase: plugin.PhaseOutput,
		Role:        plugin.RoleSink,
		Factory:     newPrintPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	return plugin.OptUnknownKeys(cfg, "print", "format")
}

type printPlugin struct {
	ip *interp.Interpolator
}

func newPrintPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	format := defaultFormat
	if v, ok := cfg["format"].(string); ok && v != "" {
		format = v
	}
	ip, err := interp.Compile(format)
	if err != nil {
		return nil, fmt.Errorf("print: invalid format pattern: %w", err)
	}
	return &printPlugin{ip: ip}, nil
}

func (p *printPlugin) Name() string        { return "print" }
func (p *printPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *printPlugin) Output(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		result, err := p.ip.Render(interp.EntryDataWithState(e))
		if err != nil {
			fmt.Printf("[print error: %v]\n", err)
			continue
		}
		fmt.Println(result)
	}
	return nil
}
