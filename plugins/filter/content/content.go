// Package content provides a filter that rejects entries whose torrent file
// listing matches unwanted patterns (e.g. *.rar, *.exe).
//
// It operates on the torrent_files field set by metainfo/torrent. Entries
// that have no torrent_files field are left undecided (the filter is a no-op
// for non-torrent entries).
//
// Config keys:
//
//	reject  - glob pattern or list of patterns; entry is rejected if any file matches
//	require - glob pattern or list of patterns; entry is rejected if no file matches
package content

import (
	"context"
	"fmt"
	"path"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "content",
		Description: "reject entries whose torrent file listing matches unwanted glob patterns",
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireOneOf(cfg, "content", "reject", "require"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "content", "reject", "require")...)
	return errs
}

type contentPlugin struct {
	reject  []string
	require []string
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	reject, err := toStringSlice(cfg["reject"])
	if err != nil {
		return nil, fmt.Errorf("content: 'reject': %w", err)
	}
	require, err := toStringSlice(cfg["require"])
	if err != nil {
		return nil, fmt.Errorf("content: 'require': %w", err)
	}
	if len(reject) == 0 && len(require) == 0 {
		return nil, fmt.Errorf("content: at least one of 'reject' or 'require' must be set")
	}
	// Validate patterns at construction time.
	for _, p := range append(reject, require...) {
		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Errorf("content: invalid glob pattern %q: %w", p, err)
		}
	}
	return &contentPlugin{reject: reject, require: require}, nil
}

func (p *contentPlugin) Name() string        { return "content" }
func (p *contentPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *contentPlugin) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	v, ok := e.Get("torrent_files")
	if !ok {
		return nil // not a torrent entry; skip
	}
	files, ok := v.([]string)
	if !ok || len(files) == 0 {
		return nil
	}

	// Reject if any file matches a reject pattern.
	for _, pat := range p.reject {
		for _, f := range files {
			// Match against the filename component only.
			base := path.Base(f)
			matched, _ := path.Match(pat, base)
			if !matched {
				// Also try full path for patterns with directory separators.
				matched, _ = path.Match(pat, f)
			}
			if matched {
				e.Reject(fmt.Sprintf("content: file %q matches reject pattern %q", f, pat))
				return nil
			}
		}
	}

	// Require: at least one file must match each require pattern.
	for _, pat := range p.require {
		found := false
		for _, f := range files {
			base := path.Base(f)
			if matched, _ := path.Match(pat, base); matched {
				found = true
				break
			}
			if matched, _ := path.Match(pat, f); matched {
				found = true
				break
			}
		}
		if !found {
			e.Reject(fmt.Sprintf("content: no file matches required pattern %q", pat))
			return nil
		}
	}

	return nil
}

func toStringSlice(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case string:
		return []string{t}, nil
	case []string:
		return t, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("non-string item in list")
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported type %T", v)
}
