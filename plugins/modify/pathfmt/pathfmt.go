// Package pathfmt renders a path pattern into a named entry field.
//
// After rendering, each path component is automatically scrubbed using
// generic (cross-platform) rules — characters invalid on Windows or commonly
// problematic on other filesystems are replaced with underscores.
package pathfmt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "pathfmt",
		Description: "render a path pattern into an entry field, scrubbing invalid characters",
		PluginPhase: plugin.PhaseModify,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "path", "pathfmt"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "field", "pathfmt"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "pathfmt", "path", "field")...)
	return errs
}

type pathfmtPlugin struct {
	ip    *interp.Interpolator
	field string
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	path, _ := cfg["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("pathfmt: 'path' is required")
	}
	ip, err := interp.Compile(path)
	if err != nil {
		return nil, fmt.Errorf("pathfmt: invalid path pattern: %w", err)
	}

	field, _ := cfg["field"].(string)
	if field == "" {
		return nil, fmt.Errorf("pathfmt: 'field' is required")
	}

	return &pathfmtPlugin{ip: ip, field: field}, nil
}

func (p *pathfmtPlugin) Name() string        { return "pathfmt" }
func (p *pathfmtPlugin) Phase() plugin.Phase { return plugin.PhaseModify }

func (p *pathfmtPlugin) Modify(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	result, err := p.ip.Render(interp.EntryData(e))
	if err != nil {
		return fmt.Errorf("pathfmt: render: %w", err)
	}
	e.Set(p.field, scrubPath(result))
	return nil
}

// scrubPath sanitizes each component of a path using generic (cross-platform)
// rules, preserving the path separator.
func scrubPath(path string) string {
	sep := string(filepath.Separator)
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		parts[i] = scrubComponent(part)
	}
	return strings.Join(parts, sep)
}

func scrubComponent(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isInvalidRune(r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	result := strings.TrimRight(b.String(), ". ")
	upper := strings.ToUpper(strings.SplitN(result, ".", 2)[0])
	if windowsReserved[upper] {
		result = result + "_"
	}
	if result == "" {
		return "_"
	}
	return result
}

func isInvalidRune(r rune) bool {
	if r < 0x20 || r == 0x7f {
		return true
	}
	if strings.ContainsRune(`<>:"/\|?*`, r) {
		return true
	}
	return !unicode.IsPrint(r)
}

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}
