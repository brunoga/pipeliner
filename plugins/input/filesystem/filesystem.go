// Package filesystem provides an input plugin that scans local directories.
package filesystem

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "filesystem",
		Description: "scan a local directory and emit one entry per file",
		PluginPhase: plugin.PhaseInput,
		Factory:     newFilesystemPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "path", "filesystem"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "filesystem", "path", "recursive", "mask")...)
	return errs
}

type filesystemPlugin struct {
	path      string
	recursive bool
	mask      string // glob pattern, e.g. "*.torrent"
}

func newFilesystemPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	path, ok := cfg["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("filesystem: 'path' is required")
	}
	recursive, _ := cfg["recursive"].(bool)
	mask, _ := cfg["mask"].(string)
	return &filesystemPlugin{path: path, recursive: recursive, mask: mask}, nil
}

func (f *filesystemPlugin) Name() string        { return "filesystem" }
func (f *filesystemPlugin) Phase() plugin.Phase { return plugin.PhaseInput }

func (f *filesystemPlugin) Run(ctx context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	var entries []*entry.Entry

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != f.path && !f.recursive {
				return filepath.SkipDir
			}
			return nil
		}

		name := d.Name()
		if f.mask != "" {
			matched, matchErr := filepath.Match(f.mask, name)
			if matchErr != nil || !matched {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		ext := filepath.Ext(name)
		e := entry.New(name, "file://"+path)
		e.Set("location", path)
		e.Set("filename", name)
		e.Set("extension", ext)
		e.Set("size", info.Size())
		e.Set("modified_time", info.ModTime())
		e.SetFileInfo(entry.FileInfo{
			GenericInfo:  entry.GenericInfo{Title: name},
			Filename:     name,
			Extension:    ext,
			Location:     path,
			FileSize:     info.Size(),
			ModifiedTime: info.ModTime(),
		})
		entries = append(entries, e)
		return nil
	}

	if err := filepath.WalkDir(f.path, walkFn); err != nil {
		return nil, fmt.Errorf("filesystem: walk %q: %w", f.path, err)
	}
	return entries, nil
}

// Stat is the os.Stat function; replaced in tests.
var Stat = os.Stat
