// Package filesystem provides an input plugin that scans local directories.
package filesystem

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "filesystem",
		Description: "scan a local directory and emit one entry per file",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldSource,
			entry.FieldTitle,
			entry.FieldFileName,
			entry.FieldFileExtension,
			entry.FieldFileLocation,
			entry.FieldFileSize,
			entry.FieldFileModifiedTime,
		},
		Factory:  newFilesystemPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "path", Type: plugin.FieldTypeString, Required: true, Hint: "Directory to scan"},
			{Key: "mask", Type: plugin.FieldTypeString, Hint: "Glob pattern, e.g. *.torrent"},
			{Key: "recursive", Type: plugin.FieldTypeBool, Hint: "Scan subdirectories"},
			{Key: "stable_for", Type: plugin.FieldTypeString, Hint: "Skip files modified more recently than this, e.g. 2m"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "path", "filesystem"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "stable_for", "filesystem"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "filesystem", "path", "recursive", "mask", "stable_for")...)
	return errs
}

type filesystemPlugin struct {
	path      string
	recursive bool
	mask      string // glob pattern, e.g. "*.torrent"
	// stableFor, when non-zero, skips a file whose content was modified
	// within that long. See the comment on Generate.
	stableFor time.Duration
}

func newFilesystemPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	path, ok := cfg["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("filesystem: 'path' is required")
	}
	recursive, _ := cfg["recursive"].(bool)
	mask, _ := cfg["mask"].(string)
	var stableFor time.Duration
	if s, ok := cfg["stable_for"].(string); ok && s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("filesystem: stable_for %q: %w", s, err)
		}
		if d < 0 {
			return nil, fmt.Errorf("filesystem: stable_for %q is negative", s)
		}
		stableFor = d
	}
	return &filesystemPlugin{path: path, recursive: recursive, mask: mask, stableFor: stableFor}, nil
}

func (f *filesystemPlugin) Name() string { return "filesystem" }

// Generate emits one entry per file under the configured path.
//
// With stable_for set, a file whose content changed more recently than that is
// skipped and picked up on a later run. This is what makes the plugin usable
// as a watcher: scheduled every few minutes against a directory things are
// delivered into, it hands each file downstream once a writer has plausibly
// finished with it. A transfer in progress keeps bumping the file's mtime, so
// it stays too young to emit; a torrent client preallocating the full size and
// filling it out of order is covered for the same reason, since the size being
// final from the start is irrelevant to the test.
//
// It is a safety net, not a completion signal, and two limits are worth
// knowing:
//
//   - A writer that restores the original mtime after writing (rsync -t on a
//     file it resumes, tar -p) can look settled while incomplete. Delivering
//     into a staging directory and renaming into the watched one is the only
//     airtight answer: a rename within a filesystem is atomic, so the watched
//     directory never contains a partial file at all.
//   - For content delivered as a directory tree, the age of any one file in it
//     says nothing about the tree. A disc's BDMV/index.bdmv is small and
//     written early, so it settles long before the streams beside it. Tree
//     completion cannot be decided from the outside; use the rename pattern.
//
// Pairing this with a downstream seen filter is what keeps a file from being
// emitted on every subsequent run. seen commits only after the sinks confirm,
// so a file whose processing failed is retried rather than silently dropped.
func (f *filesystemPlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	var entries []*entry.Entry
	// One clock reading for the whole walk, so a long scan cannot decide two
	// equally-aged files differently.
	cutoff := Now().Add(-f.stableFor)

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

		if f.stableFor > 0 && info.ModTime().After(cutoff) {
			if tc != nil && tc.Logger != nil {
				tc.Logger.Debug("filesystem: skipping file still settling",
					"path", path, "modified", info.ModTime(), "stable_for", f.stableFor)
			}
			return nil
		}

		ext := filepath.Ext(name)
		e := entry.New(name, "file://"+path)
		e.SetFileInfo(entry.FileInfo{
			GenericInfo:  entry.GenericInfo{Title: name},
			Filename:     name,
			Extension:    ext,
			Location:     path,
			FileSize:     info.Size(),
			ModifiedTime: info.ModTime(),
		})
		e.Set(entry.FieldSource, "filesystem:"+f.path)
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

// Now is the clock stable_for is measured against; replaced in tests so a
// settling window can be exercised without sleeping.
var Now = time.Now
