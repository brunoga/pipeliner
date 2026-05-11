// Package content provides a filter that rejects entries whose file listing
// matches unwanted patterns (e.g. *.rar, *.exe).
//
// File list resolution order:
//  1. torrent_files — populated by metainfo_torrent (from the .torrent file)
//     or metainfo_magnet (via DHT resolution). Most complete.
//  2. file_location basename — set by the filesystem plugin for local files.
//  3. URL path component — last segment of the entry URL, for direct-download
//     entries where the URL itself reveals the file type (e.g. .../file.rar).
//
// When none of the above yields a file list the check is skipped and a Warn
// is logged so the gap is visible. Fallback sources (2, 3) are logged at
// Debug so they are visible with --log-level debug.
//
// Config keys:
//
//	reject  - glob pattern or list of patterns; entry is rejected if any file matches
//	require - glob pattern or list of patterns; entry is rejected if no file matches
package content

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "content",
		Description: "reject entries whose torrent file listing matches unwanted glob patterns",
		PluginPhase: plugin.PhaseFilter,
		Role:        plugin.RoleProcessor,
		Requires:    []string{entry.FieldTorrentFiles},
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "reject", Type: plugin.FieldTypeList, Hint: "Glob patterns; entry rejected if any file matches, e.g. *.rar"},
			{Key: "require", Type: plugin.FieldTypeList, Hint: "Glob patterns; entry rejected if no file matches, e.g. *.mkv"},
		},
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

func (p *contentPlugin) Filter(_ context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	files, source := resolveFiles(e)
	if len(files) == 0 {
		tc.Logger.Warn("content: file list unavailable — check skipped",
			"entry", e.URL,
			"hint", "run metainfo_torrent or metainfo_magnet before content to populate torrent_files")
		return nil
	}
	if source != "torrent_files" {
		tc.Logger.Debug("content: using fallback file source",
			"entry", e.URL,
			"source", source,
			"files", files)
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

// resolveFiles returns the file list to check and the source it came from.
// It tries torrent_files first, then file_location, then the URL path.
func resolveFiles(e *entry.Entry) ([]string, string) {
	// Primary: torrent_files (metainfo_torrent / metainfo_magnet via DHT).
	if v, ok := e.Get(entry.FieldTorrentFiles); ok {
		if files, ok := v.([]string); ok && len(files) > 0 {
			return files, "torrent_files"
		}
	}
	// Fallback 1: file_location basename (filesystem plugin).
	if loc := e.GetString(entry.FieldFileLocation); loc != "" {
		return []string{path.Base(loc)}, "file_location"
	}
	// Fallback 2: URL path component (direct-download entries).
	if name := urlBasename(e.URL); name != "" {
		return []string{name}, "url"
	}
	return nil, ""
}

// urlBasename returns the last meaningful path segment of a URL, or "" if the
// URL has no useful filename (e.g. query-only URLs like Jackett proxy links).
func urlBasename(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || strings.HasPrefix(rawURL, "magnet:") {
		return ""
	}
	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		return ""
	}
	return name
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
