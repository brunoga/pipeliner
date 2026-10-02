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
	// stableFor, when non-zero, withholds an item until its contents have
	// been observed unchanged for at least this long. See Generate.
	stableFor time.Duration
	db        *store.SQLiteStore
}

func newFilesystemPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
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
	if stableFor > 0 && db == nil {
		return nil, fmt.Errorf("filesystem: stable_for needs a database to record what it observed")
	}
	return &filesystemPlugin{
		path: path, recursive: recursive, mask: mask, stableFor: stableFor, db: db,
	}, nil
}

func (f *filesystemPlugin) Name() string { return "filesystem" }

// Generate emits one entry per file under the configured path.
//
// With stable_for set, a file is withheld until the item it belongs to has
// been observed twice, unchanged, across that window. This is what makes the
// plugin a watch folder: scheduled every few minutes against a directory
// things are delivered into, it hands each file downstream once its delivery
// has stopped changing.
//
// The unit of settling is the **item** — the top-level child of the watched
// directory — not the individual file, because that is the unit things arrive
// in: a download client, a torrent and an rsync each produce one directory (or
// one file) per release. A disc delivered as a BDMV tree therefore settles as
// a whole, so matching mask="index.bdmv" yields one entry that appears only
// once the streams beside it have finished arriving. The mask decides what is
// emitted; it plays no part in deciding what has settled.
//
// Why two observations rather than a timestamp age: a single observation
// cannot tell a finished delivery from a paused one, and it cannot see a tree
// that will have more files in it later. Timestamps are also not trustworthy
// on their own — rsync -t restores the source's modification time, and rsync
// -a implies it, so a file that arrived seconds ago can carry a timestamp days
// old. Comparing contents across the window does not care: a change has to
// hide from both size and timestamp, for every file in the item, to go
// unnoticed.
//
// The cost is one scan interval of latency, and a database record per item in
// flight. The records are pruned as items leave.
//
// Pairing this with a downstream seen filter is what keeps a settled file from
// being emitted on every subsequent run. seen commits only after the sinks
// confirm, so a file whose processing failed is retried rather than silently
// dropped.
func (f *filesystemPlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	files, err := f.scan(ctx)
	if err != nil {
		return nil, err
	}

	emit := func(s scanned) *entry.Entry {
		name := filepath.Base(s.path)
		e := entry.New(name, "file://"+s.path)
		e.SetFileInfo(entry.FileInfo{
			GenericInfo:  entry.GenericInfo{Title: name},
			Filename:     name,
			Extension:    filepath.Ext(name),
			Location:     s.path,
			FileSize:     s.size,
			ModifiedTime: s.modTime,
		})
		e.Set(entry.FieldSource, "filesystem:"+f.path)
		return e
	}

	if f.stableFor == 0 {
		var entries []*entry.Entry
		for _, s := range files {
			if s.matches {
				entries = append(entries, emit(s))
			}
		}
		return entries, nil
	}

	items := groupItems(files)
	ss := newSettleStore(f.db.Bucket(f.bucketName(tc)), f.path)
	// One clock reading for the whole scan, so a slow walk cannot decide two
	// items that changed at the same moment differently.
	now := Now()

	var entries []*entry.Entry
	for _, it := range items {
		ok, err := ss.settled(it, f.stableFor, now)
		if err != nil {
			return nil, fmt.Errorf("filesystem: %w", err)
		}
		if !ok {
			if tc != nil && tc.Logger != nil {
				tc.Logger.Debug("filesystem: item still settling",
					"item", it.name, "files", len(it.files), "stable_for", f.stableFor)
			}
			continue
		}
		for _, s := range it.files {
			if s.matches {
				entries = append(entries, emit(s))
			}
		}
	}

	if err := ss.prune(items); err != nil {
		// Pruning is housekeeping: a stale record costs a row, not a wrong
		// answer, so it must not fail a scan that otherwise worked.
		if tc != nil && tc.Logger != nil {
			tc.Logger.Warn("filesystem: pruning settle snapshots", "err", err)
		}
	}
	return entries, nil
}

// bucketName scopes the settle snapshots to the task, so two pipelines
// watching one directory keep independent observations.
func (f *filesystemPlugin) bucketName(tc *plugin.TaskContext) string {
	name := ""
	if tc != nil {
		name = tc.Name
	}
	return SettleBucketPrefix + name
}

// scan walks the configured path once, returning every file it visited.
//
// The mask is recorded rather than applied, because the two consumers need
// different things: emission wants only matching files, while settling wants
// everything that might still be arriving.
func (f *filesystemPlugin) scan(ctx context.Context) ([]scanned, error) {
	var out []scanned
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
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(f.path, path)
		if err != nil {
			return nil
		}
		matches := true
		if f.mask != "" {
			m, matchErr := filepath.Match(f.mask, d.Name())
			matches = matchErr == nil && m
		}
		out = append(out, scanned{
			path: path, rel: rel, size: info.Size(),
			modTime: info.ModTime(), matches: matches,
		})
		return nil
	}
	if err := filepath.WalkDir(f.path, walkFn); err != nil {
		return nil, fmt.Errorf("filesystem: walk %q: %w", f.path, err)
	}
	return out, nil
}

// Stat is the os.Stat function; replaced in tests.
var Stat = os.Stat

// Now is the clock stable_for is measured against; replaced in tests so a
// settling window can be exercised without sleeping.
var Now = time.Now
