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
		Role:        plugin.RoleSink,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "command", Type: plugin.FieldTypePattern, Required: true, Hint: "Shell command with {field} interpolation, e.g. notify-send {title}"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "command", "exec"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "exec", "command")...)
	return errs
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

func (p *execPlugin) deliver(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
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
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...) //nolint:gosec // intentional: user-configured command
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return err
	}
	return nil
}

func (p *execPlugin) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	return p.deliver(ctx, tc, entry.FilterAccepted(entries))
}
