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
			{Key: "command", Type: plugin.FieldTypePattern, Required: true, Hint: "Program to run, with {field} interpolation. Quote arguments containing spaces, e.g. convert \"{file_location}\""},
			{Key: "args", Type: plugin.FieldTypeList, Hint: "Arguments, one argv element each — no splitting, so spaces need no quoting"},
			{Key: "ignore_errors", Type: plugin.FieldTypeBool, Hint: "Keep the entry accepted when the command exits non-zero (default: fail it)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "command", "exec"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "exec", "command", "args", "ignore_errors")...)
	return errs
}

type execPlugin struct {
	ip *interp.Interpolator
	// argIPs is non-nil when the config supplied an explicit args list. Then
	// `command` is the program verbatim — never split — and each element here
	// renders to exactly one argv entry.
	argIPs       []*interp.Interpolator
	ignoreErrors bool
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
	p := &execPlugin{ip: ip, ignoreErrors: plugin.OptBool(cfg, "ignore_errors", false)}
	for i, a := range plugin.ToStringSlice(cfg["args"]) {
		aip, err := interp.Compile(a)
		if err != nil {
			return nil, fmt.Errorf("exec: invalid args[%d] pattern: %w", i, err)
		}
		p.argIPs = append(p.argIPs, aip)
	}
	return p, nil
}

func (p *execPlugin) Name() string { return "exec" }

func (p *execPlugin) deliver(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		argv, err := p.argvFor(e)
		if err != nil {
			p.report(tc, e, err)
			continue
		}
		if err := p.run(ctx, argv); err != nil {
			tc.Logger.Error("exec: command failed", "cmd", argv[0], "args", argv[1:], "err", err)
			p.report(tc, e, err)
		}
	}
	return nil
}

// report fails the entry unless the config opted out. A sink that only logged
// its failures let the commit phase record the entry as delivered, so a
// tracker upstream marked the item done and never retried it — the one thing
// the commit phase exists to prevent.
func (p *execPlugin) report(tc *plugin.TaskContext, e *entry.Entry, err error) {
	if p.ignoreErrors {
		return
	}
	e.Fail("exec: " + err.Error())
}

// argvFor renders the command for one entry. With an explicit args list the
// program is taken verbatim and every argument is one argv element; otherwise
// the rendered command is split quote-aware.
func (p *execPlugin) argvFor(e *entry.Entry) ([]string, error) {
	data := interp.EntryData(e)
	cmdStr, err := p.ip.Render(data)
	if err != nil {
		return nil, fmt.Errorf("render command: %w", err)
	}
	if p.argIPs != nil {
		prog := strings.TrimSpace(cmdStr)
		if prog == "" {
			return nil, fmt.Errorf("empty command")
		}
		argv := make([]string, 0, len(p.argIPs)+1)
		argv = append(argv, prog)
		for i, aip := range p.argIPs {
			a, err := aip.Render(data)
			if err != nil {
				return nil, fmt.Errorf("render args[%d]: %w", i, err)
			}
			argv = append(argv, a)
		}
		return argv, nil
	}
	argv, err := splitArgs(cmdStr)
	if err != nil {
		return nil, fmt.Errorf("parse command %q: %w", cmdStr, err)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return argv, nil
}

// run executes argv directly. No shell is involved on any platform: the
// rendered string carries release titles straight from an indexer, and a shell
// would make them executable. Running argv directly is also what makes this
// portable — there is no sh on Windows, and exec.Command applies each OS's own
// argument quoting.
func (p *execPlugin) run(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // intentional: user-configured command
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
	return p.deliver(ctx, tc, entries)
}
