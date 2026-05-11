// Package decompress provides an output plugin that extracts archive files
// (RAR, ZIP, 7z) to a destination directory using system tools. No CGo or
// external Go libraries are required.
//
// Tool selection priority: unrar → 7z → unar. A specific tool can be forced
// via the "tool" config key. The plugin fails at construction time if none of
// the supported tools is found on PATH.
//
// Config keys:
//
//	to             - destination directory (required)
//	keep_dirs      - preserve internal directory structure (default: true)
//	delete_archive - remove archive file(s) after successful extraction (default: false)
//	tool           - force a specific tool: "unrar", "7z", or "unar" (default: auto)
package decompress

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "decompress",
		Description: "extract archive files (RAR, ZIP, 7z) to a destination directory",
		PluginPhase: plugin.PhaseOutput,
		Role:        plugin.RoleSink,
		Requires:    []string{entry.FieldFileLocation},
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "to", "decompress"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "tool", "decompress", "unrar", "7z", "unar"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "decompress", "to", "keep_dirs", "delete_archive", "tool")...)
	return errs
}

// supportedTools lists extraction tools in preference order.
var supportedTools = []string{"unrar", "7z", "unar"}

type decompressPlugin struct {
	to            string
	keepDirs      bool
	deleteArchive bool
	tool          string // resolved tool name ("unrar", "7z", or "unar")
	toolPath      string // absolute path to the binary
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	to, _ := cfg["to"].(string)
	if to == "" {
		return nil, fmt.Errorf("decompress: 'to' destination directory is required")
	}

	keepDirs := true
	if v, ok := cfg["keep_dirs"].(bool); ok {
		keepDirs = v
	}
	deleteArchive, _ := cfg["delete_archive"].(bool)

	forceTool, _ := cfg["tool"].(string)

	tool, toolPath, err := resolveTool(forceTool)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}

	return &decompressPlugin{
		to:            to,
		keepDirs:      keepDirs,
		deleteArchive: deleteArchive,
		tool:          tool,
		toolPath:      toolPath,
	}, nil
}

func (p *decompressPlugin) Name() string        { return "decompress" }
func (p *decompressPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *decompressPlugin) Output(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		if err := p.extract(ctx, tc, e); err != nil {
			tc.Logger.Error("decompress: extraction failed", "entry", e.Title, "err", err)
			e.Fail(err.Error())
		}
	}
	return nil
}

func (p *decompressPlugin) extract(ctx context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	archivePath := archiveLocation(e)
	if archivePath == "" {
		return fmt.Errorf("no archive path found on entry (need 'location' or URL ending in .rar/.zip/.7z)")
	}

	if err := os.MkdirAll(p.to, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	args := p.buildArgs(archivePath)
	cmd := exec.CommandContext(ctx, p.toolPath, args...) //nolint:gosec
	cmd.Stdout = nil
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", p.tool, err, stderr.String())
	}

	if p.deleteArchive {
		p.removeArchive(archivePath)
	}
	return nil
}

func (p *decompressPlugin) buildArgs(archivePath string) []string {
	switch p.tool {
	case "unrar":
		if p.keepDirs {
			return []string{"x", "-y", archivePath, p.to + string(filepath.Separator)}
		}
		return []string{"e", "-y", archivePath, p.to + string(filepath.Separator)}
	case "7z":
		flag := "e"
		if p.keepDirs {
			flag = "x"
		}
		return []string{flag, "-y", "-o" + p.to, archivePath}
	case "unar":
		args := []string{archivePath, "-output-directory", p.to, "-force-overwrite"}
		if !p.keepDirs {
			args = append(args, "-no-directory")
		}
		return args
	}
	return nil
}

// removeArchive deletes the primary archive and any split parts (.r00, .r01, …).
func (p *decompressPlugin) removeArchive(archivePath string) {
	_ = os.Remove(archivePath)

	// Remove split RAR parts (.r00–.r99 siblings).
	ext := strings.ToLower(filepath.Ext(archivePath))
	if ext == ".rar" {
		base := archivePath[:len(archivePath)-4]
		for i := range 100 {
			part := fmt.Sprintf("%s.r%02d", base, i)
			if err := os.Remove(part); err != nil {
				break // stop at first missing part
			}
		}
	}
}

// archiveLocation returns the local filesystem path of the archive to extract.
func archiveLocation(e *entry.Entry) string {
	if loc := e.GetString(entry.FieldFileLocation); loc != "" {
		return loc
	}
	u := e.URL
	lower := strings.ToLower(u)
	for _, ext := range []string{".rar", ".zip", ".7z"} {
		if strings.HasSuffix(lower, ext) {
			return u
		}
	}
	return ""
}

// resolveTool finds an appropriate extraction tool on PATH.
func resolveTool(force string) (name, path string, err error) {
	candidates := supportedTools
	if force != "" {
		if !slices.Contains(supportedTools, force) {
			return "", "", fmt.Errorf("unsupported tool %q; choose from: %s",
				force, strings.Join(supportedTools, ", "))
		}
		candidates = []string{force}
	}

	for _, t := range candidates {
		if p, err := exec.LookPath(t); err == nil {
			return t, p, nil
		}
	}
	return "", "", fmt.Errorf("no extraction tool found; install one of: %s",
		strings.Join(candidates, ", "))
}
