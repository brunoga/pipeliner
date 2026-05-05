// Package exists rejects entries whose title matches a file already on disk.
package exists

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "exists",
		Description: "reject entries whose title matches a file already present on disk",
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	if err := plugin.RequireString(cfg, "path", "exists"); err != nil {
		return []error{err}
	}
	return nil
}

type existsPlugin struct {
	path      string
	recursive bool

	mu    sync.Mutex
	index map[string]bool // built once per task run on first Filter call
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	path, _ := cfg["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("exists: 'path' is required")
	}
	recursive, _ := cfg["recursive"].(bool)
	return &existsPlugin{path: path, recursive: recursive}, nil
}

func (p *existsPlugin) Name() string        { return "exists" }
func (p *existsPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *existsPlugin) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	index, err := p.getIndex()
	if err != nil {
		return fmt.Errorf("exists: build index: %w", err)
	}

	if index[normalize(e.Title)] {
		e.Reject(fmt.Sprintf("file matching %q already exists in %s", e.Title, p.path))
		return nil
	}

	// Also check the filename field if set.
	if filename := e.GetString("filename"); filename != "" {
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		if index[normalize(base)] {
			e.Reject(fmt.Sprintf("file %q already exists in %s", filename, p.path))
		}
	}
	return nil
}

// getIndex returns the cached index, building it on first call.
func (p *existsPlugin) getIndex() (map[string]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.index != nil {
		return p.index, nil
	}
	idx, err := buildIndex(p.path, p.recursive)
	if err != nil {
		return nil, err
	}
	p.index = idx
	return p.index, nil
}

// buildIndex walks path and returns a set of normalized base names (without extension).
func buildIndex(root string, recursive bool) (map[string]bool, error) {
	index := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if path != root && !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		base = strings.TrimSuffix(base, filepath.Ext(base))
		index[normalize(base)] = true
		return nil
	})
	return index, err
}

// normalize lowercases and collapses common word separators so that
// "My.Show.S01E01" matches "My Show S01E01" and similar variants.
func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r == '.' || r == '_' || r == '-' {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
