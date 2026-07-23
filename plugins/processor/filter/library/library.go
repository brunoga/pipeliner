// Package library rejects entries whose episode or movie already exists in
// the actual media library on disk, at equal-or-better quality. Unlike the
// seen filter (which knows what pipeliner grabbed) this checks disk truth,
// so it catches content acquired outside pipeliner and enables real quality
// upgrades: an entry strictly better than the library copy passes through.
//
// The filesystem backend walks the configured paths and parses video
// filenames with the same release-name parsers the pipeline uses
// (internal/series, internal/movies), caching the resulting index in memory
// and refreshing it when older than ttl. The plex and jellyfin backends
// build the same index from the server's API instead; they compare by
// resolution only, since that is the quality signal those APIs expose.
package library

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/mediaserver"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "library"

// defaultExtensions are the video file extensions indexed by the filesystem
// backend. Overridable via the extensions config key.
var defaultExtensions = []string{".mkv", ".mp4", ".avi", ".m4v", ".ts", ".wmv"}

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "reject entries already in the media library at equal-or-better quality; better releases pass as upgrades",
		Role:        plugin.RoleProcessor,
		Requires:    plugin.RequireAll(entry.FieldTitle),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "paths", Type: plugin.FieldTypeList, Required: true, Hint: "Library directories to index (walked recursively)"},
			{Key: "backend", Type: plugin.FieldTypeString, Default: "filesystem", Hint: "Library backend: filesystem (paths), plex or jellyfin (url + token)"},
			{Key: "ttl", Type: plugin.FieldTypeDuration, Default: "15m", Hint: "How long the disk index is reused before rescanning"},
			{Key: "upgrade", Type: plugin.FieldTypeBool, Default: true, Hint: "Pass entries whose quality is strictly better than the library copy"},
			{Key: "extensions", Type: plugin.FieldTypeList, Hint: "Video file extensions to index (default: common video types; filesystem backend only)"},
			{Key: "url", Type: plugin.FieldTypeString, Hint: "Media server base URL (plex/jellyfin backends)"},
			{Key: "token", Type: plugin.FieldTypeString, Hint: "Media server API token (plex/jellyfin backends)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptUnknownKeys(cfg, pluginName, "paths", "backend", "ttl", "upgrade", "extensions", "url", "token"); err != nil {
		errs = append(errs, err...)
	}
	backend, _ := cfg["backend"].(string)
	switch backend {
	case "", "filesystem":
		if paths := toStringSlice(cfg["paths"]); len(paths) == 0 {
			errs = append(errs, fmt.Errorf("%s: 'paths' must list at least one library directory", pluginName))
		}
	case "plex", "jellyfin":
		if u, _ := cfg["url"].(string); u == "" {
			errs = append(errs, fmt.Errorf("%s: backend %q requires 'url'", pluginName, backend))
		}
		if t, _ := cfg["token"].(string); t == "" {
			errs = append(errs, fmt.Errorf("%s: backend %q requires 'token'", pluginName, backend))
		}
	default:
		errs = append(errs, fmt.Errorf("%s: unsupported backend %q (supported: filesystem, plex, jellyfin)", pluginName, backend))
	}
	if err := plugin.OptDuration(cfg, "ttl", pluginName); err != nil {
		errs = append(errs, err)
	}
	return errs
}

// indexEntry records the best quality seen in the library for one item.
type indexEntry struct {
	Quality quality.Quality
	Path    string // representative file, for reject reasons and debugging
}

type libraryPlugin struct {
	paths      []string
	extensions map[string]bool
	ttl        time.Duration
	upgrade    bool

	mu      sync.Mutex
	series  map[string]indexEntry // NormalizeName(show) + "|" + episodeID
	movies  map[string]indexEntry // NormalizeTitle(title) + "|" + year ("|0" when unknown)
	builtAt time.Time

	// walk is swappable in tests (filesystem backend).
	walk func(root string, fn fs.WalkDirFunc) error
	// client, when non-nil, replaces the filesystem walk (plex/jellyfin).
	client mediaserver.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	backend, _ := cfg["backend"].(string)
	if backend == "" {
		backend = "filesystem"
	}

	var client mediaserver.Client
	paths := toStringSlice(cfg["paths"])
	if backend == "filesystem" {
		if len(paths) == 0 {
			return nil, fmt.Errorf("%s: 'paths' must list at least one library directory", pluginName)
		}
	} else {
		url, _ := cfg["url"].(string)
		token, _ := cfg["token"].(string)
		if url == "" || token == "" {
			return nil, fmt.Errorf("%s: backend %q requires 'url' and 'token'", pluginName, backend)
		}
		c, err := mediaserver.New(backend, url, token)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pluginName, err)
		}
		client = c
	}

	ttl := 15 * time.Minute
	if s, ok := cfg["ttl"].(string); ok && s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid ttl: %w", pluginName, err)
		}
		ttl = d
	}

	exts := toStringSlice(cfg["extensions"])
	if len(exts) == 0 {
		exts = defaultExtensions
	}
	extSet := make(map[string]bool, len(exts))
	for _, x := range exts {
		if !strings.HasPrefix(x, ".") {
			x = "." + x
		}
		extSet[strings.ToLower(x)] = true
	}

	upgrade := true
	if v, ok := cfg["upgrade"].(bool); ok {
		upgrade = v
	}

	return &libraryPlugin{
		paths:      paths,
		extensions: extSet,
		ttl:        ttl,
		upgrade:    upgrade,
		walk:       filepath.WalkDir,
		client:     client,
	}, nil
}

func (p *libraryPlugin) Name() string { return pluginName }

// ensureIndex rebuilds the disk index when it is missing or older than ttl.
func (p *libraryPlugin) ensureIndex(tc *plugin.TaskContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.series != nil && time.Since(p.builtAt) < p.ttl {
		return
	}

	seriesIdx := make(map[string]indexEntry)
	movieIdx := make(map[string]indexEntry)

	if p.client != nil {
		items, err := p.client.ListItems(context.Background())
		if err != nil {
			// Keep the previous index (possibly stale) rather than treating
			// an unreachable server as an empty library — an empty index
			// would wave every duplicate through.
			tc.Logger.Warn(pluginName+": media server unreachable, keeping previous index", "err", err)
			if p.series == nil {
				p.series, p.movies = seriesIdx, movieIdx
			}
			p.builtAt = time.Now()
			return
		}
		for _, it := range items {
			q := quality.Parse(it.Resolution)
			switch it.Type {
			case "episode":
				if it.Show == "" {
					continue
				}
				key := series.NormalizeName(it.Show) + "|" + it.EpisodeID()
				if cur, ok := seriesIdx[key]; !ok || q.Better(cur.Quality) {
					seriesIdx[key] = indexEntry{Quality: q, Path: it.Show + " " + it.EpisodeID()}
				}
			case "movie":
				if it.Title == "" {
					continue
				}
				key := movies.NormalizeTitle(it.Title) + "|" + fmt.Sprint(it.Year)
				if cur, ok := movieIdx[key]; !ok || q.Better(cur.Quality) {
					movieIdx[key] = indexEntry{Quality: q, Path: it.Title}
				}
			}
		}
		p.series, p.movies, p.builtAt = seriesIdx, movieIdx, time.Now()
		tc.Logger.Info(pluginName+": indexed media server",
			"episodes", len(seriesIdx), "movies", len(movieIdx))
		return
	}

	files := 0
	for _, root := range p.paths {
		err := p.walk(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// Unreadable subtree: skip it, keep indexing the rest.
				tc.Logger.Debug(pluginName+": walk error", "path", path, "err", err)
				return nil
			}
			if d.IsDir() || !p.extensions[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			files++
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			q := quality.Parse(name)
			if ep, ok := series.Parse(name); ok && ep.SeriesName != "" {
				key := series.NormalizeName(ep.SeriesName) + "|" + series.EpisodeID(ep)
				if cur, ok := seriesIdx[key]; !ok || q.Better(cur.Quality) {
					seriesIdx[key] = indexEntry{Quality: q, Path: path}
				}
				return nil
			}
			if mv, ok := movies.Parse(name); ok && mv.Title != "" {
				key := movies.NormalizeTitle(mv.Title) + "|" + fmt.Sprint(mv.Year)
				if cur, ok := movieIdx[key]; !ok || q.Better(cur.Quality) {
					movieIdx[key] = indexEntry{Quality: q, Path: path}
				}
			}
			return nil
		})
		if err != nil {
			tc.Logger.Warn(pluginName+": walk failed", "root", root, "err", err)
		}
	}
	p.series, p.movies, p.builtAt = seriesIdx, movieIdx, time.Now()
	tc.Logger.Info(pluginName+": indexed library",
		"files", files, "episodes", len(seriesIdx), "movies", len(movieIdx))
}

// lookup finds the library copy matching e, if any.
func (p *libraryPlugin) lookup(e *entry.Entry) (indexEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if epID := e.GetString(entry.FieldSeriesEpisodeID); epID != "" {
		if hit, ok := p.series[series.NormalizeName(e.Title)+"|"+epID]; ok {
			return hit, true
		}
		// Title may still be the decorated release name; parse it the same
		// way the index parsed filenames.
		if ep, ok := series.Parse(e.Title); ok && ep.SeriesName != "" {
			if hit, ok := p.series[series.NormalizeName(ep.SeriesName)+"|"+epID]; ok {
				return hit, true
			}
		}
	}
	if e.GetString(entry.FieldMediaType) == "movie" || e.GetString(entry.FieldSeriesEpisodeID) == "" {
		title := movies.NormalizeTitle(e.Title)
		year, _ := e.Fields[entry.FieldVideoYear].(int)
		if hit, ok := p.movies[title+"|"+fmt.Sprint(year)]; ok {
			return hit, true
		}
		// A release title that still carries year/quality decoration won't
		// normalize to the same key as a parsed filename; parse it the same
		// way the index parsed the file.
		if mv, ok := movies.Parse(e.Title); ok {
			if hit, ok := p.movies[movies.NormalizeTitle(mv.Title)+"|"+fmt.Sprint(mv.Year)]; ok {
				return hit, true
			}
		}
	}
	return indexEntry{}, false
}

// Process implements plugin.ProcessorPlugin.
func (p *libraryPlugin) Process(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	p.ensureIndex(tc)
	for _, e := range entries {
		hit, ok := p.lookup(e)
		if !ok {
			continue
		}
		eq, hasQ := e.Quality()
		if hasQ && p.upgrade && eq.Better(hit.Quality) {
			tc.Logger.Info(pluginName+": upgrade candidate",
				"entry", e.Title, "library", hit.Quality.String(), "release", eq.String())
			continue
		}
		e.Reject(fmt.Sprintf("%s: already in library at %s (%s)",
			pluginName, hit.Quality.String(), filepath.Base(hit.Path)))
	}
	return entry.PassThrough(entries), nil
}

func toStringSlice(v any) []string {
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, x := range vv {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
