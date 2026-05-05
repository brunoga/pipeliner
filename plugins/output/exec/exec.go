// Package exec runs a shell command for each accepted entry.
//
// Commands use {field} or {field:format} syntax. Go template syntax ({{.field}})
// is also accepted for backward compatibility.
package exec

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "exec",
		Description: "run a shell command for each accepted entry",
		PluginPhase: plugin.PhaseOutput,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	if err := plugin.RequireString(cfg, "command", "exec"); err != nil {
		return []error{err}
	}
	return nil
}

type execPlugin struct {
	ip *interp.Interpolator
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	command, _ := cfg["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("exec: 'command' is required")
	}
	ip, err := interp.Compile(command)
	if err != nil {
		return nil, fmt.Errorf("exec: invalid command pattern: %w", err)
	}
	return &execPlugin{ip: ip}, nil
}

func (p *execPlugin) Name() string        { return "exec" }
func (p *execPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *execPlugin) Output(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		cmdStr, err := p.ip.Render(interp.EntryData(e))
		if err != nil {
			tc.Logger.Error("exec: render command", "title", e.Title, "err", err)
			continue
		}
		if err := p.run(ctx, cmdStr); err != nil {
			tc.Logger.Error("exec: command failed", "cmd", cmdStr, "err", err)
		}
	}
	return nil
}

func (p *execPlugin) run(ctx context.Context, cmdStr string) error {
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return err
	}
	return nil
}
