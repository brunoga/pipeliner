// Package movies provides a movie filter and learn plugin.
//
// It parses movie title, year, quality, and 3D format from entry titles,
// matches against a configured title list, and enforces quality constraints.
// Multiple quality variants of the same movie are all accepted so the dedup
// processor can choose the best copy. The tracker is updated via CommitPlugin
// after all sinks confirm, so only successfully downloaded movies are recorded.
//
// Fields set on each matched entry: movie_title, movie_year, movie_quality,
// movie_3d.
//
// The movie list may be provided statically via 'static', dynamically via 'list'
// (a list of input plugins whose entry titles are used as movie names), or both.
// Dynamic lists are cached for the configured ttl (default: 1h).
package movies

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "movies",
		Description: "accept movies from a configured list; track downloads across runs",
		Role:        plugin.RoleProcessor,
		Produces: []string{
			entry.FieldMovieTitle,
			entry.FieldVideoYear,
			entry.FieldVideoQuality,
			entry.FieldVideoResolution,
			entry.FieldVideoSource,
			entry.FieldVideoIs3D,
		},
		Factory:     newPlugin,
		Validate:    validate,
		AcceptsList: true,
		Schema: []plugin.FieldSchema{
			{Key: "static", Type: plugin.FieldTypeList, Hint: "Static list of movie titles"},
			{Key: "list", Type: plugin.FieldTypeDict, Hint: "Dynamic list from a source plugin (e.g. trakt_list)"},
			{Key: "quality", Type: plugin.FieldTypeString, Hint: "Quality spec, e.g. 1080p+ webrip+"},
			{Key: "ttl", Type: plugin.FieldTypeDuration, Default: "1h", Hint: "Cache TTL for dynamic lists"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject movies not in the list"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireOneOf(cfg, "movies", "static", "list"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "ttl", "movies"); err != nil {
		errs = append(errs, err)
	}
	if q, _ := cfg["quality"].(string); q != "" {
		if _, err := quality.ParseSpec(q); err != nil {
			errs = append(errs, fmt.Errorf("movies: invalid quality spec: %w", err))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "movies", "static", "list", "ttl", "quality", "reject_unmatched")...)
	return errs
}

type moviesPlugin struct {
	staticTitles    []string // normalised movie titles from config
	listSources     []plugin.SourcePlugin
	listCache       *cache.Cache[[]string]
	spec            quality.Spec
	tracker         *imovies.Tracker
	rejectUnmatched bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	raw := toStringSlice(cfg["static"])
	staticTitles := make([]string, len(raw))
	for i, s := range raw {
		staticTitles[i] = match.Normalize(s)
	}

	listRaw, _ := cfg["list"].([]any)
	var listSources []plugin.SourcePlugin
	for _, item := range listRaw {
		src, err := plugin.MakeFromPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("movies: list: %w", err)
		}
		listSources = append(listSources, src)
	}

	if len(staticTitles) == 0 && len(listSources) == 0 {
		return nil, fmt.Errorf("movies: at least one of 'static' or 'list' is required")
	}

	ttl := time.Hour
	if v, _ := cfg["ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("movies: invalid ttl %q: %w", v, err)
		}
		ttl = d
	}

	var spec quality.Spec
	if q, _ := cfg["quality"].(string); q != "" {
		s, err := quality.ParseSpec(q)
		if err != nil {
			return nil, fmt.Errorf("movies: invalid quality spec: %w", err)
		}
		spec.MinResolution = s.MinResolution
		spec.MinSource = s.MinSource
		spec.MinCodec = s.MinCodec
		spec.MinAudio = s.MinAudio
		spec.MinColorRange = s.MinColorRange
		spec.MinFormat3D = s.MinFormat3D
		spec.MaxFormat3D = s.MaxFormat3D
	}

	rejectUnmatched := true
	if v, ok := cfg["reject_unmatched"]; ok {
		rejectUnmatched, _ = v.(bool)
	}

	return &moviesPlugin{
		staticTitles:    staticTitles,
		listSources:     listSources,
		listCache:       cache.NewPersistent[[]string](ttl, db.Bucket("cache_movies_list")),
		spec:            spec,
		rejectUnmatched: rejectUnmatched,
		tracker:      imovies.NewTracker(db.Bucket("movies")),
	}, nil
}

func (p *moviesPlugin) Name() string        { return "movies" }

func (p *moviesPlugin) filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	m, ok := imovies.Parse(e.Title)
	if !ok {
		if p.rejectUnmatched {
			e.Reject("movies: title did not parse as movie")
		}
		return nil
	}

	titles := p.resolveTitles(ctx, tc)
	matchedTitle, ok := matchTitle(m.Title, titles)
	if !ok {
		if p.rejectUnmatched {
			e.Reject("movies: title not in list")
		}
		return nil
	}

	is3D := m.Quality.Format3D != quality.Format3DNone
	qualStr := m.Quality.String()
	mi := entry.MovieInfo{}
	mi.Title = matchedTitle
	mi.Year = m.Year
	mi.Is3D = is3D
	if qualStr != "unknown" {
		mi.Quality = qualStr
		mi.Resolution = m.Quality.ResolutionName()
		mi.Source = m.Quality.SourceName()
	}
	e.SetMovieInfo(mi)

	// Check the quality spec first — the spec is an absolute gate for this
	// task and must never be bypassed, even for REPACK/PROPER upgrades.
	// Without this ordering, a non-3D film already recorded by the flat
	// `movies` task (is3D=false) would be found by the `movies-3d` task's
	// IsSeen lookup and accepted as a REPACK upgrade, skipping the 3D check.
	if (p.spec != quality.Spec{}) && !p.spec.Matches(m.Quality) {
		e.Reject(fmt.Sprintf("movies: %s (%d) quality %s does not match spec",
			matchedTitle, m.Year, m.Quality.String()))
		return nil
	}

	if p.tracker.IsSeen(matchedTitle, m.Year, is3D) {
		if rec, ok := p.tracker.Latest(matchedTitle, is3D); ok && rec.Year == m.Year {
			betterQuality := m.Quality.Better(rec.Quality)
			properOrRepack := m.Proper || m.Repack
			notDowngrade := !rec.Quality.Better(m.Quality)
			if betterQuality || (properOrRepack && notDowngrade) {
				e.Accept()
				return nil
			}
		}
		e.Reject(fmt.Sprintf("movies: %s (%d) already downloaded", matchedTitle, m.Year))
		return nil
	}

	e.Accept()
	return nil
}

func (p *moviesPlugin) persist(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	titles := p.resolveTitles(ctx, tc)
	for _, e := range entries {
		m, ok := imovies.Parse(e.Title)
		if !ok {
			continue
		}
		matchedTitle, ok := matchTitle(m.Title, titles)
		if !ok {
			continue
		}
		is3D := m.Quality.Format3D != quality.Format3DNone
		year := m.Year
		if year == 0 {
			year = e.GetInt(entry.FieldVideoYear)
		}
		if err := p.tracker.Mark(imovies.Record{
			Title:   matchedTitle,
			Year:    year,
			Is3D:    is3D,
			Quality: m.Quality,
		}); err != nil {
			return fmt.Errorf("movies: mark %s (%d): %w", matchedTitle, year, err)
		}
	}
	return nil
}

func (p *moviesPlugin) resolveTitles(ctx context.Context, tc *plugin.TaskContext) []string {
	return plugin.ResolveDynamicList(ctx, tc, p.listSources, p.staticTitles,
		func(src string) ([]string, bool) { return p.listCache.Get(src) },
		func(src string, v []string) { p.listCache.Set(src, v) },
		match.Normalize,
	)
}

func matchTitle(parsed string, titles []string) (string, bool) {
	norm := match.Normalize(parsed)
	for _, title := range titles {
		if match.Fuzzy(norm, title) {
			return title, true
		}
	}
	return "", false
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

func (p *moviesPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("movies filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}

// Commit implements plugin.CommitPlugin. It persists movie tracking records
// for entries that were not failed by any downstream sink.
func (p *moviesPlugin) Commit(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	return p.persist(ctx, tc, entries)
}
