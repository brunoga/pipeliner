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
	"github.com/brunoga/pipeliner/internal/downloads"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/settle"
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
			{Key: "static", Type: plugin.FieldTypeList, Hint: "Optional static list of movie titles; omit to accept every classified movie"},
			{Key: "list", Type: plugin.FieldTypeDict, Hint: "Optional dynamic list from a source plugin (e.g. trakt_list); omit to accept every classified movie"},
			{Key: "ttl", Type: plugin.FieldTypeDuration, Default: "1h", Hint: "Cache TTL for dynamic lists"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject entries not classified as movie upstream; when a list is configured, also reject entries whose title isn't in the list"},
			{Key: "settle", Type: plugin.FieldTypeDuration, Hint: "Delay between first seeing a download-worthy release for a title and grabbing one, so a wave of increasingly better releases yields a single download of the best (0 = grab on sight)"},
			{Key: "upgrade_window", Type: plugin.FieldTypeDuration, Hint: "Accept quality upgrades only within this window after the first download (e.g. 30d as 720h; default: unlimited)"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_movies_list", Display: "Movies Title List Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	// static and list are both optional. With neither set, the filter accepts
	// every classified movie that passes the quality spec and tracker checks.
	if err := plugin.OptDuration(cfg, "ttl", "movies"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "upgrade_window", "movies"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "settle", "movies"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "movies", "static", "list", "ttl", "reject_unmatched", "upgrade_window", "settle")...)
	return errs
}

type moviesPlugin struct {
	staticTitles    []match.TitleEntry // movie titles from config (year=0 for plain strings)
	listSources     []plugin.SourcePlugin
	listCache       *cache.Cache[[]match.TitleEntry]
	tracker         *imovies.Tracker
	downloadLog     *downloads.Log
	rejectUnmatched bool
	upgradeWindow   time.Duration // 0 = upgrades accepted forever
	settle          time.Duration // 0 = grab on sight
	settleTracker   *settle.Tracker
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	raw := plugin.ToStringSlice(cfg["static"])
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

	ttl := time.Hour
	if v, _ := cfg["ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("movies: invalid ttl %q: %w", v, err)
		}
		ttl = d
	}

	rejectUnmatched := plugin.OptBool(cfg, "reject_unmatched", true)

	var upgradeWindow time.Duration
	if v, _ := cfg["upgrade_window"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("movies: invalid upgrade_window %q: %w", v, err)
		}
		upgradeWindow = d
	}

	var settleWindow time.Duration
	if v, _ := cfg["settle"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("movies: invalid settle %q: %w", v, err)
		}
		settleWindow = d
	}

	return &moviesPlugin{
		staticTitles:    staticTitles,
		settle:          settleWindow,
		settleTracker:   settle.New(db.Bucket(settle.MovieBucketName)),
		listSources:     listSources,
		listCache:       cache.NewPersistent[[]match.TitleEntry](ttl, db.Bucket("cache_movies_list")),
		rejectUnmatched: rejectUnmatched,
		upgradeWindow:   upgradeWindow,
		tracker:         imovies.NewTracker(db.Bucket(imovies.TrackerBucketName)),
		downloadLog:     downloads.New(db.Bucket(downloads.BucketName)),
	}, nil
}

func (p *moviesPlugin) Name() string { return "movies" }

// hasList reports whether the filter has any source of movie titles — either
// a static list or one or more dynamic list source plugins. When false, the
// filter accepts every classified movie that passes the quality / tracker
// checks instead of matching against a list.
func (p *moviesPlugin) hasList() bool {
	return len(p.staticTitles) > 0 || len(p.listSources) > 0
}

func (p *moviesPlugin) filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	parsedTitle := e.GetString(entry.FieldTitle)
	year := e.GetInt(entry.FieldVideoYear)
	if parsedTitle == "" {
		if p.rejectUnmatched {
			e.Reject("movies: entry has no title (not classified as movie upstream)")
		}
		return nil
	}

	// When no list is configured the filter operates in accept-all mode:
	// every classified movie passes the upstream Requires + quality / tracker
	// checks. The tracker key is the normalized parsed title so dedup and
	// upgrade detection still work across runs.
	var matchedTitle string
	if p.hasList() {
		var ok bool
		matchedTitle, ok = matchTitle(parsedTitle, year, p.resolveTitles(ctx, tc))
		if !ok {
			if p.rejectUnmatched {
				e.Reject("movies: title not in list")
			}
			return nil
		}
	} else {
		matchedTitle = match.Normalize(parsedTitle)
		if matchedTitle == "" {
			return nil
		}
	}

	q, _ := e.Quality()
	is3D := e.GetBool(entry.FieldVideoIs3D)
	properOrRepack := e.GetBool(entry.FieldVideoProper) || e.GetBool(entry.FieldVideoRepack)

	// Stamp the matched (normalized) title for persist() to read back at
	// commit time, so we don't have to re-resolve the list there.
	e.Set(moviesTrackerName, matchedTitle)

	if p.tracker.IsSeen(matchedTitle, year, is3D) {
		// LatestNearYear (rather than Latest + exact-year check) so the
		// upgrade decision still runs when the stored record's year drifts
		// from the incoming year by ±1 — theatrical vs. home-video release.
		if rec, ok := p.tracker.LatestNearYear(matchedTitle, year, is3D); ok {
			// Outside the upgrade window a better copy no longer replaces the
			// library one — a 4K re-release years later should not re-download
			// a film that was watched long ago.
			if p.upgradeWindow > 0 && !rec.DownloadedAt.IsZero() &&
				time.Since(rec.DownloadedAt) > p.upgradeWindow {
				e.Reject(fmt.Sprintf("movies: %s (%d) already downloaded (upgrade window expired)", matchedTitle, year))
				return nil
			}
			switch quality.Decide(q, rec.Quality, properOrRepack, rec.Repack) {
			case quality.UpgradeQuality:
				if p.holdForSettle(e, matchedTitle, year, is3D) {
					return nil
				}
				e.Accept(fmt.Sprintf("movies: %s (%d) quality upgrade", matchedTitle, year))
				return nil
			case quality.UpgradeProperRepack:
				if p.holdForSettle(e, matchedTitle, year, is3D) {
					return nil
				}
				e.Accept(fmt.Sprintf("movies: %s (%d) proper/repack accepted", matchedTitle, year))
				return nil
			}
		}
		e.Reject(fmt.Sprintf("movies: %s (%d) already downloaded", matchedTitle, year))
		return nil
	}

	if p.holdForSettle(e, matchedTitle, year, is3D) {
		return nil
	}
	e.Accept(fmt.Sprintf("movies: %s (%d) matched", matchedTitle, year))
	return nil
}

// holdForSettle rejects an otherwise-downloadable entry while its title's
// settle window is still running, and reports whether it did. Every release
// in a wave is held, so when the window elapses they all become eligible in
// the same run and the downstream dedup picks the single best one — instead
// of downloading each rung of the 1080p → 2160p → HDR → Atmos ladder as it
// appears. Rejection is per-run and uncommitted, so the entry is re-evaluated
// on the next run.
func (p *moviesPlugin) holdForSettle(e *entry.Entry, title string, year int, is3D bool) bool {
	key := settle.MovieKey(title, year, is3D)
	left := p.settleTracker.Offer(key, settle.CandidateOf(e), p.settle, time.Now())
	if left <= 0 {
		if p.settle > 0 {
			// Offer only reports "no time left" for a window that already
			// existed, so this release waited one out. Recorded on the entry
			// so the failure and download logs can show what settling cost.
			e.Set(entry.FieldSettled, true)
		}
		return false
	}
	e.Reject(fmt.Sprintf("movies: %s (%d) settling for %s more (holding the best release seen so far)",
		title, year, left.Round(time.Minute)))
	return true
}

// releaseSettled downloads winners whose window has elapsed but that are no
// longer advertised by any source. Indexer feeds are shallow — commonly the
// newest ~50 items, a few hours' worth — so a wave can scroll out entirely
// before a longer window expires. Rebuilding the recorded winner here means
// the wait never costs the download. Entries produced this way flow through
// filter() like any other, so the tracker records them at commit time.
func (p *moviesPlugin) releaseSettled(ctx context.Context, tc *plugin.TaskContext, batch []*entry.Entry) []*entry.Entry {
	if p.settle <= 0 {
		return nil
	}
	present := make(map[string]bool, len(batch))
	for _, e := range batch {
		present[e.URL] = true
	}
	var revived []*entry.Entry
	for _, exp := range p.settleTracker.Expired(p.settle, time.Now()) {
		if present[exp.Best.URL] {
			continue // still advertised; it goes through the normal path
		}
		e := exp.Best.Rebuild()
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
		e.Set(entry.FieldSettledRevived, true)
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("movies: settled release", "entry", e.Title, "err", err)
			continue
		}
		if !e.IsAccepted() {
			continue
		}
		tc.Logger.Info("movies: downloading settled release no longer in any feed",
			"entry", e.Title, "quality", exp.Best.Quality.String())
		revived = append(revived, e)
	}
	return revived
}

// moviesTrackerName is the entry field used to carry the matched (normalized)
// movie title from filter() to persist(). The constant lives in the entry
// package because the torrent sinks' grab records also read it (failed-grab
// recovery needs the tracker key to un-track a movie).
const moviesTrackerName = entry.FieldMoviesTrackerTitle

func (p *moviesPlugin) persist(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
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
		now := time.Now()
		if err := p.tracker.Mark(imovies.Record{
			Title:        matchedTitle,
			Year:         year,
			Is3D:         is3D,
			Repack:       properOrRepack,
			Quality:      q,
			DownloadedAt: now,
		}); err != nil {
			return fmt.Errorf("movies: mark %s (%d): %w", matchedTitle, year, err)
		}
		// The wave produced a download; the next one starts a fresh timer.
		p.settleTracker.Clear(settle.MovieKey(matchedTitle, year, is3D))
		// Best-effort append to the download history audit log. Optional: nil
		// in tests that build the plugin struct directly.
		if p.downloadLog == nil {
			continue
		}
		if err := p.downloadLog.Append(downloads.Event{
			MediaType:    "movie",
			Name:         matchedTitle,
			DisplayName:  e.GetString(entry.FieldTitle),
			Year:         year,
			Is3D:         is3D,
			Quality:      q,
			Repack:       properOrRepack,
			DownloadedAt: now,
			Settled:      e.GetBool(entry.FieldSettled),
			Revived:      e.GetBool(entry.FieldSettledRevived),
			Task:         tc.Name,
		}); err != nil {
			tc.Logger.Warn("movies: append download log", "title", matchedTitle, "year", year, "err", err)
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

func (p *moviesPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		// Movie classifier: every entry that reaches this filter is a
		// movie (Requires guarantees title + video_year + _quality).
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("movies filter error", "entry", e.Title, "err", err)
		}
	}
	entries = append(entries, p.releaseSettled(ctx, tc, entries)...)
	return entry.PassThrough(entries), nil
}

// Commit implements plugin.CommitPlugin. It persists movie tracking records
// for entries that were not failed by any downstream sink.
func (p *moviesPlugin) Commit(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	return p.persist(ctx, tc, entries)
}
