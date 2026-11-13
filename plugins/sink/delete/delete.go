// Package delete removes the file each entry names, and the folders that
// leaves empty.
//
// It deletes only below the configured roots, and never a root itself: an
// entry whose file_location is outside every root fails instead. Folders
// emptied by a deletion are removed up to, not including, the root, so a
// library folder such as /data/media/3d-mvc-inbox survives being emptied --
// the mistake a "rmdir -p" would make.
//
// Only files are deleted. A directory, or a path that does not exist, fails
// the entry; a symbolic link is removed as a link, never followed.
package delete

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "delete"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "delete the file at file_location and the folders that leaves empty, never outside the given roots",
		Role:        plugin.RoleSink,
		Requires:    plugin.RequireAll(entry.FieldFileLocation),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "roots", Type: plugin.FieldTypeList, Required: true, Hint: "Directories below which files may be deleted; the roots themselves are never removed"},
			{Key: "prune_empty", Type: plugin.FieldTypeBool, Hint: "Remove folders a deletion leaves empty, up to the root (default true)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	roots := plugin.ToStringSlice(cfg["roots"])
	if len(roots) == 0 {
		errs = append(errs, fmt.Errorf("%s: 'roots' must list at least one directory", pluginName))
	}
	for _, r := range roots {
		if !filepath.IsAbs(r) || filepath.Clean(r) == string(filepath.Separator) {
			errs = append(errs, fmt.Errorf("%s: root %q must be an absolute path other than /", pluginName, r))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, "roots", "prune_empty")...)
	return errs
}

type deletePlugin struct {
	roots      []string
	pruneEmpty bool
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	if errs := validate(cfg); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	p := &deletePlugin{pruneEmpty: true}
	for _, r := range plugin.ToStringSlice(cfg["roots"]) {
		p.roots = append(p.roots, filepath.Clean(r))
	}
	if v, ok := cfg["prune_empty"].(bool); ok {
		p.pruneEmpty = v
	}
	return p, nil
}

func (p *deletePlugin) Name() string { return pluginName }

func (p *deletePlugin) Consume(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	for _, e := range entries {
		if err := p.remove(e.GetString(entry.FieldFileLocation)); err != nil {
			tc.Logger.Error(pluginName+": not deleted", "entry", e.Title, "err", err)
			e.Fail(err.Error())
			continue
		}
		tc.Logger.Info(pluginName+": deleted", "file", e.GetString(entry.FieldFileLocation))
	}
	return nil
}

func (p *deletePlugin) remove(loc string) error {
	if loc == "" || !filepath.IsAbs(loc) {
		return fmt.Errorf("file_location %q is not an absolute path", loc)
	}
	path := filepath.Clean(loc)
	root, ok := p.rootOf(path)
	if !ok {
		return fmt.Errorf("%s is not below any of the roots %v", path, p.roots)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory; only files are deleted", path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if p.pruneEmpty {
		pruneUpTo(filepath.Dir(path), root)
	}
	return nil
}

// rootOf returns the root path lies strictly below.
func (p *deletePlugin) rootOf(path string) (string, bool) {
	for _, r := range p.roots {
		if strings.HasPrefix(path, r+string(filepath.Separator)) {
			return r, true
		}
	}
	return "", false
}

// pruneUpTo removes dir and its parents while they are empty, stopping at
// root, which is never removed.
func pruneUpTo(dir, root string) {
	for dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return
		}
		dir = filepath.Dir(dir)
	}
}
