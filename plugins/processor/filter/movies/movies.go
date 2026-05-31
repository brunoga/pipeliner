// Package movies provides a movie filter and learn plugin.
//
// It reads movie metadata (title, year, quality, 3D, PROPER/REPACK markers)
// from entry fields populated upstream by metainfo_file (or any equivalent
// source), matches against a configured title list, and enforces quality
// constraints. Multiple quality variants of the same movie are all accepted so
// the dedup processor can choose the best copy. The tracker is updated via
// CommitPlugin after all sinks confirm, so only successfully downloaded movies
// are recorded.
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
		// Movie metadata must be populated upstream — by metainfo_file in the
		// common case, or by any other plugin that sets these fields.
		// FieldQuality (the typed quality.Quality struct read via e.Quality())
		// is required so spec matching and upgrade detection work.
		Requires: plugin.RequireAll(
			entry.FieldTitle,
			entry.FieldVideoYear,
			entry.FieldQuality,
		),
		// Every entry exiting this filter is a movie by construction.
		// Setting media_type here makes the classification Certain for
		// downstream nodes like dedup.
		Produces: []string{
			entry.FieldMediaType,
		},
		Factory:     newPlugin,
		Validate:    validate,
		AcceptsList: true,
		Schema: []plugin.FieldSchema{
			{Key: "static", Type: plugin.FieldTypeList, Hint: "Static list of movie titles"},
			{Key: "list", Type: plugin.FieldTypeDict, Hint: "Dynamic list from a source plugin (e.g. trakt_list)"},
			{Key: "quality", Type: plugin.FieldTypeString, Hint: "Quality spec, e.g. 1080p+ (floor), 1080p (exact), 720p-1080p (range)"},
			{Key: "ttl", Type: plugin.FieldTypeDuration, Default: "1h", Hint: "Cache TTL for dynamic lists"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject entries not classified as movie upstream or not in the configured list"},
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
	staticTitles    []match.TitleEntry // movie titles from config (year=0 for plain strings)
	listSources     []plugin.SourcePlugin
	listCache       *cache.Cache[[]match.TitleEntry]
	spec            quality.Spec
	tracker         *imovies.Tracker
	rejectUnmatched bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	raw := toStringSlice(cfg["static"])
	staticTitles := make([]match.TitleEntry, len(raw))
	for i, s := range raw {
		staticTitles[i] = match.NewTitleEntry(s, 0) // static titles have no year
	}

	listRaw, _ := cfg["list"].([]any)
	var listSources []plugin.SourcePlugin
	for _, item := range listRaw {
		src, err := plugin.MakeListPlugin(item, db)
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
		spec = s
	}

	rejectUnmatched := true
	if v, ok := cfg["reject_unmatched"]; ok {
		rejectUnmatched, _ = v.(bool)
	}

	return &moviesPlugin{
		staticTitles:    staticTitles,
		listSources:     listSources,
		listCache:       cache.NewPersistent[[]match.TitleEntry](ttl, db.Bucket("cache_movies_list")),
		spec:            spec,
		rejectUnmatched: rejectUnmatched,
		tracker:         imovies.NewTracker(db.Bucket("movies")),
	}, nil
}

func (p *moviesPlugin) Name() string { return "movies" }

func (p *moviesPlugin) filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	parsedTitle := e.GetString(entry.FieldTitle)
	year := e.GetInt(entry.FieldVideoYear)
	if parsedTitle == "" {
		if p.rejectUnmatched {
			e.Reject("movies: entry has no title (not classified as movie upstream)")
		}
		return nil
	}

	titles := p.resolveTitles(ctx, tc)
	matchedTitle, ok := matchTitle(parsedTitle, year, titles)
	if !ok {
		if p.rejectUnmatched {
			e.Reject("movies: title not in list")
		}
		return nil
	}

	q, _ := e.Quality()
	is3D := e.GetBool(entry.FieldVideoIs3D)
	properOrRepack := e.GetBool(entry.FieldVideoProper) || e.GetBool(entry.FieldVideoRepack)

	// Stamp the matched (normalized) title for persist() to read back at
	// commit time, so we don't have to re-resolve the list there.
	e.Set(moviesTrackerName, matchedTitle)

	// Check the quality spec first — the spec is an absolute gate for this
	// task and must never be bypassed, even for REPACK/PROPER upgrades.
	// Without this ordering, a non-3D film already recorded by the flat
	// `movies` task (is3D=false) would be found by the `movies-3d` task's
	// IsSeen lookup and accepted as a REPACK upgrade, skipping the 3D check.
	if (p.spec != quality.Spec{}) && !p.spec.Matches(q) {
		e.Reject(fmt.Sprintf("movies: %s (%d) quality %s does not match spec",
			matchedTitle, year, q.String()))
		return nil
	}

	if p.tracker.IsSeen(matchedTitle, year, is3D) {
		if rec, ok := p.tracker.Latest(matchedTitle, is3D); ok && rec.Year == year {
			betterQuality := q.Better(rec.Quality)
			notDowngrade := !rec.Quality.Better(q)
			// Allow a REPACK/PROPER only when the stored version was not already
			// a REPACK at the same quality; otherwise the same torrent would be
			// accepted on every pipeline run indefinitely.
			if betterQuality || (properOrRepack && !rec.Repack && notDowngrade) {
				reason := fmt.Sprintf("movies: %s (%d) quality upgrade", matchedTitle, year)
				if properOrRepack && !betterQuality {
					reason = fmt.Sprintf("movies: %s (%d) proper/repack accepted", matchedTitle, year)
				}
				e.Accept(reason)
				return nil
			}
		}
		e.Reject(fmt.Sprintf("movies: %s (%d) already downloaded", matchedTitle, year))
		return nil
	}

	e.Accept(fmt.Sprintf("movies: %s (%d) matched", matchedTitle, year))
	return nil
}

// moviesTrackerName is the entry field used to carry the matched (normalized)
// movie title from filter() to persist(). It is internal to this plugin.
const moviesTrackerName = "_movies_tracker_title"

func (p *moviesPlugin) persist(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		// Only persist entries that were accepted by all downstream nodes.
		// The executor passes every entry the movies node produced to Commit,
		// including those later rejected by dedup — we must filter them here
		// so the stored quality reflects the entry that was actually downloaded.
		if !e.IsAccepted() {
			continue
		}
		matchedTitle := e.GetString(moviesTrackerName)
		if matchedTitle == "" {
			continue
		}
		year := e.GetInt(entry.FieldVideoYear)
		is3D := e.GetBool(entry.FieldVideoIs3D)
		q, _ := e.Quality()
		properOrRepack := e.GetBool(entry.FieldVideoProper) || e.GetBool(entry.FieldVideoRepack)
		if err := p.tracker.Mark(imovies.Record{
			Title:   matchedTitle,
			Year:    year,
			Is3D:    is3D,
			Repack:  properOrRepack,
			Quality: q,
		}); err != nil {
			return fmt.Errorf("movies: mark %s (%d): %w", matchedTitle, year, err)
		}
	}
	return nil
}

func (p *moviesPlugin) resolveTitles(ctx context.Context, tc *plugin.TaskContext) []match.TitleEntry {
	return plugin.ResolveDynamicList(ctx, tc, p.listSources, p.staticTitles,
		func(src string) ([]match.TitleEntry, bool) { return p.listCache.Get(src) },
		func(src string, v []match.TitleEntry) { p.listCache.Set(src, v) },
	)
}

// matchTitle returns the normalised title from the list that matches the
// candidate (title + year). Year-aware: if both the candidate and a list entry
// carry a year, they must be within 1 of each other.
func matchTitle(parsed string, year int, titles []match.TitleEntry) (string, bool) {
	norm := match.Normalize(parsed)
	for _, t := range titles {
		if match.FuzzyEntry(norm, year, t) {
			return t.Norm, true
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
		// Movie classifier: every entry that reaches this filter is a
		// movie (Requires guarantees title + video_year + _quality).
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
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
