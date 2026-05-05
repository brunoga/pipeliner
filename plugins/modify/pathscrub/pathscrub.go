// Package pathscrub provides a modify plugin that sanitizes entry fields so
// their values are safe to use as filesystem path components.
package pathscrub

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "pathscrub",
		Description: "sanitize entry fields for use as safe filesystem path components",
		PluginPhase: plugin.PhaseModify,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptEnum(cfg, "target", "pathscrub", "windows", "linux", "generic"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "pathscrub", "target", "fields")...)
	return errs
}

// windowsReserved lists names that are reserved on Windows regardless of extension.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

type scrubPlugin struct {
	fields []string
	target string // "windows", "linux", or "generic"
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	target, _ := cfg["target"].(string)
	if target == "" {
		target = "generic"
	}
	if target != "windows" && target != "linux" && target != "generic" {
		return nil, fmt.Errorf("pathscrub: 'target' must be windows, linux, or generic")
	}

	fields := toStringSlice(cfg["fields"])
	if len(fields) == 0 {
		fields = []string{"download_path"}
	}

	return &scrubPlugin{fields: fields, target: target}, nil
}

func (p *scrubPlugin) Name() string        { return "pathscrub" }
func (p *scrubPlugin) Phase() plugin.Phase { return plugin.PhaseModify }

func (p *scrubPlugin) Modify(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	for _, field := range p.fields {
		v, ok := e.Get(field)
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		e.Set(field, scrubPath(s, p.target))
	}
	return nil
}

// scrubPath sanitizes each component of a path, preserving the OS separator.
func scrubPath(path, target string) string {
	sep := string(filepath.Separator)
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		parts[i] = scrubComponent(part, target)
	}
	return strings.Join(parts, sep)
}

// scrubComponent sanitizes a single path component (no separators).
func scrubComponent(s, target string) string {
	if s == "" {
		return s
	}

	var b strings.Builder
	for _, r := range s {
		if isInvalid(r, target) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	result := b.String()

	if target == "windows" || target == "generic" {
		// Trim trailing dots and spaces (Windows rejects them).
		result = strings.TrimRight(result, ". ")
		// Rename reserved device names.
		upper := strings.ToUpper(strings.SplitN(result, ".", 2)[0])
		if windowsReserved[upper] {
			result = result + "_"
		}
	}

	if result == "" {
		return "_"
	}
	return result
}

func isInvalid(r rune, target string) bool {
	// Control characters — always invalid.
	if r < 0x20 || r == 0x7f {
		return true
	}
	switch target {
	case "windows", "generic":
		// Windows forbidden characters.
		if strings.ContainsRune(`<>:"/\|?*`, r) {
			return true
		}
	case "linux":
		// Only null and forward-slash are truly invalid on Linux.
		if r == '/' || r == 0 {
			return true
		}
	}
	// Additional generic check: non-printable.
	if !unicode.IsPrint(r) {
		return true
	}
	return false
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Scrub sanitizes s for the given target filesystem ("windows", "linux", "generic").
// Exported so the template functions package can reuse the logic.
func Scrub(s, target string) string {
	return scrubComponent(s, target)
}
