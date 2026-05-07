// Package movies provides a movie filter and learn plugin.
//
// It parses movie title, year, quality, and 3D format from entry titles,
// matches against a configured title list, and enforces quality constraints.
// Multiple quality variants of the same movie are all accepted so the task
// engine's automatic deduplication can choose the best copy. The tracker is
// updated in the Learn phase so only the dedup survivor is recorded.
//
// Fields set on each matched entry: movie_title, movie_year, movie_quality,
// movie_3d.
//
// The movie list may be provided statically via 'static', dynamically via 'from'
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
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireOneOf(cfg, "movies", "static", "from"); err != nil {
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
	errs = append(errs, plugin.OptUnknownKeys(cfg, "movies", "static", "from", "ttl", "quality")...)
	return errs
}

type moviesPlugin struct {
	staticTitles []string          // normalised movie titles from config
	from         []plugin.InputPlugin
	listCache    *cache.Cache[[]string]
	spec         quality.Spec
	tracker      *imovies.Tracker
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	raw := toStringSlice(cfg["static"])
	staticTitles := make([]string, len(raw))
	for i, s := range raw {
		staticTitles[i] = match.Normalize(s)
	}

	fromRaw, _ := cfg["from"].([]any)
	var froms []plugin.InputPlugin
	for _, item := range fromRaw {
		inp, err := plugin.MakeFromPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("movies: from: %w", err)
		}
		froms = append(froms, inp)
	}

	if len(staticTitles) == 0 && len(froms) == 0 {
		return nil, fmt.Errorf("movies: at least one of 'static' or 'from' is required")
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
	}

	return &moviesPlugin{
		staticTitles: staticTitles,
		from:         froms,
		listCache:    cache.NewPersistent[[]string](ttl, db.Bucket("cache_movies_from")),
		spec:         spec,
		tracker:      imovies.NewTracker(db.Bucket("movies")),
	}, nil
}

func (p *moviesPlugin) Name() string        { return "movies" }
func (p *moviesPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *moviesPlugin) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	m, ok := imovies.Parse(e.Title)
	if !ok {
		tc.Logger.Debug("movies: title did not parse as movie", "entry", e.Title)
		return nil
	}

	titles := p.resolveTitles(ctx, tc)
	matchedTitle, ok := matchTitle(m.Title, titles)
	if !ok {
		tc.Logger.Debug("movies: no title match", "title", m.Title, "titles_loaded", len(titles))
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

	if (p.spec != quality.Spec{}) && !p.spec.Matches(m.Quality) {
		e.Reject(fmt.Sprintf("movies: %s (%d) quality %s does not match spec",
			matchedTitle, m.Year, m.Quality.String()))
		return nil
	}

	e.Accept()
	return nil
}

func (p *moviesPlugin) Learn(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	titles := p.resolveTitles(ctx, tc)
	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		m, ok := imovies.Parse(e.Title)
		if !ok {
			continue
		}
		matchedTitle, ok := matchTitle(m.Title, titles)
		if !ok {
			continue
		}
		is3D := m.Quality.Format3D != quality.Format3DNone
		if err := p.tracker.Mark(imovies.Record{
			Title:   matchedTitle,
			Year:    m.Year,
			Is3D:    is3D,
			Quality: m.Quality,
		}); err != nil {
			return fmt.Errorf("movies: mark %s (%d): %w", matchedTitle, m.Year, err)
		}
	}
	return nil
}

func (p *moviesPlugin) resolveTitles(ctx context.Context, tc *plugin.TaskContext) []string {
	return plugin.ResolveDynamicList(ctx, tc, p.from, p.staticTitles,
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
