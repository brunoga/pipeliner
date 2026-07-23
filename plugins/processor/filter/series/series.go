// Package series provides a TV series processor that accepts episodes from a
// configured show list and tracks downloads across runs.
//
// Episode metadata is read from entry fields populated upstream by
// metainfo_file (or any equivalent metainfo source). The plugin does not
// parse the entry title itself; the upstream requirement is declared via
// Descriptor.Requires so the DAG validator catches misconfigured pipelines
// at load time.
//
// The plugin matches the parsed series name against the configured show list,
// enforces optional quality and ordering constraints, and persists downloads
// via CommitPlugin so only entries that survive all downstream sinks are
// recorded. Multiple quality variants of the same episode are accepted so the
// dedup processor can pick the best copy.
//
// The show list may be provided statically via 'static', dynamically via 'list'
// (source plugins whose entry titles are used as show names), or both.
// Dynamic lists are cached for the configured ttl (default: 1h).
package series

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "series",
		Description: "accept episodes for configured shows; track downloads across runs",
		Role:        plugin.RoleProcessor,
		// Episode metadata must be populated upstream — by metainfo_file in
		// the common case, or by any other plugin that sets these fields.
		// series_season and series_episode are part of the same parsed-episode
		// bundle as series_episode_id (metainfo_file always sets them
		// together); they support follow-mode season-floor logic and
		// double-episode part marking on commit. Declaring them keeps the
		// contract symmetric with the premiere plugin and documents the
		// expected upstream shape.
		// FieldQuality (the typed quality.Quality struct read via e.Quality())
		// is required so spec matching and upgrade detection work; without it
		// quality features silently degrade to no-op.
		Requires: plugin.RequireAll(
			entry.FieldTitle,
			entry.FieldSeriesEpisodeID,
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			entry.FieldQuality,
		),
		// Every entry exiting this filter is a series episode by
		// construction (the filter only accepts entries that match a known
		// show). Setting media_type here makes the classification Certain
		// for downstream nodes like dedup, instead of relying on
		// metainfo_file's conditional MayProduce.
		Produces: []string{
			entry.FieldMediaType,
		},
		Factory:     newPlugin,
		Validate:    validate,
		AcceptsList: true,
		Schema: []plugin.FieldSchema{
			{Key: "static", Type: plugin.FieldTypeList, Hint: "Optional static list of show names to accept; omit to accept every classified episode"},
			{Key: "list", Type: plugin.FieldTypeDict, Hint: "Optional dynamic show list from a source plugin (e.g. tvdb_favorites, trakt_list); omit to accept every classified episode"},
			{Key: "tracking", Type: plugin.FieldTypeEnum, Enum: []string{"strict", "backfill", "follow"}, Default: "strict", Hint: "Episode ordering mode"},
			{Key: "ttl", Type: plugin.FieldTypeDuration, Default: "1h", Hint: "Cache TTL for dynamic lists"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject episodes not classified as series upstream; when a list is configured, also reject episodes whose show isn't in the list"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_series_list", Display: "Series Title List Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	// static and list are both optional. With neither set, the filter accepts
	// every classified episode that passes the quality spec and tracker checks
	// — useful for "download every 720p+ episode I find" pipelines.
	if err := plugin.OptDuration(cfg, "ttl", "series"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "tracking", "series", "strict", "backfill", "follow"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "series", "static", "list", "ttl", "tracking", "reject_unmatched")...)
	return errs
}

// tracking controls how episode ordering is enforced.
type tracking string

const (
	trackingStrict   tracking = "strict"   // reject if episode number skips > 1 ahead of latest
	trackingBackfill tracking = "backfill" // accept any episode not yet downloaded
	trackingFollow   tracking = "follow"   // accept all on first encounter; thereafter reject episodes from seasons older than the highest tracked episode
)

// seriesTrackerName is the entry field used to carry the normalized matched
// show name from filter() to persist(). It is internal to this plugin.
const seriesTrackerName = "_series_tracker_name"

type seriesPlugin struct {
	staticShows     []match.TitleEntry // show names from config (year=0 for plain strings)
	listSources     []plugin.SourcePlugin
	listCache       *cache.Cache[[]match.TitleEntry]
	tracking        tracking
	tracker         *series.Tracker
	inactive        *series.InactiveSet
	rejectUnmatched bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	raw := plugin.ToStringSlice(cfg["static"])
	staticShows := make([]match.TitleEntry, len(raw))
	for i, s := range raw {
		staticShows[i] = match.NewTitleEntry(s, 0) // static show names have no year
	}

	listRaw, _ := cfg["list"].([]any)
	var listSources []plugin.SourcePlugin
	for _, item := range listRaw {
		src, err := plugin.MakeListPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("series: list: %w", err)
		}
		listSources = append(listSources, src)
	}

	ttl := time.Hour
	if v, _ := cfg["ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("series: invalid ttl %q: %w", v, err)
		}
		ttl = d
	}

	tracker := series.NewTracker(db.Bucket(series.TrackerBucketName))

	tr := trackingStrict
	if t, _ := cfg["tracking"].(string); t != "" {
		switch tracking(t) {
		case trackingStrict, trackingBackfill, trackingFollow:
			tr = tracking(t)
		default:
			return nil, fmt.Errorf("series: unknown tracking mode %q (strict|backfill|follow)", t)
		}
	}

	rejectUnmatched := plugin.OptBool(cfg, "reject_unmatched", true)

	return &seriesPlugin{
		staticShows:     staticShows,
		listSources:     listSources,
		listCache:       cache.NewPersistent[[]match.TitleEntry](ttl, db.Bucket("cache_series_list")),
		tracking:        tr,
		tracker:         tracker,
		inactive:        series.NewInactiveSet(db.Bucket(series.InactiveBucketName)),
		rejectUnmatched: rejectUnmatched,
	}, nil
}

func (p *seriesPlugin) Name() string { return "series" }

// hasList reports whether the filter has any source of show names — either a
// static list or one or more dynamic list source plugins. When false, the
// filter accepts every classified entry that passes the quality / tracker
// checks instead of matching against a list.
func (p *seriesPlugin) hasList() bool {
	return len(p.staticShows) > 0 || len(p.listSources) > 0
}

func (p *seriesPlugin) filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	epID := e.GetString(entry.FieldSeriesEpisodeID)
	if epID == "" {
		if p.rejectUnmatched {
			e.Reject("series: entry has no series_episode_id (not classified as series upstream)")
		}
		return nil
	}

	parsedName := e.GetString(entry.FieldTitle)
	// When no list is configured the filter operates in accept-all mode:
	// every classified episode passes the upstream Requires + quality/tracker
	// checks, with no title matching. The tracker key is the normalized parsed
	// name so dedup and upgrade detection still work across runs.
	var matchedShow string
	if p.hasList() {
		var ok bool
		matchedShow, ok = matchShow(parsedName, p.resolveShows(ctx, tc))
		if !ok {
			if p.rejectUnmatched {
				e.Reject("series: show not in list")
			}
			return nil
		}
	} else {
		matchedShow = match.Normalize(parsedName)
		if matchedShow == "" {
			return nil
		}
	}

	e.Set(seriesTrackerName, matchedShow)

	// Deactivated shows (series_tracker_update sink, typically after a
	// series_lifecycle "complete" classification) are rejected before any
	// quality or tracker checks — searching for them is wasted work.
	if rec, ok := p.inactive.Get(matchedShow); ok {
		reason := rec.Reason
		if reason == "" {
			reason = "deactivated"
		}
		e.Reject(fmt.Sprintf("series: %s inactive (%s)", matchedShow, reason))
		return nil
	}

	incomingQuality, _ := e.Quality()

	if stored, ok := p.tracker.Get(matchedShow, epID); ok {
		properOrRepack := e.GetBool(entry.FieldVideoProper) || e.GetBool(entry.FieldVideoRepack)
		switch quality.Decide(incomingQuality, stored.Quality, properOrRepack, stored.Repack) {
		case quality.UpgradeQuality:
			e.Accept(fmt.Sprintf("series: %s %s quality upgrade", matchedShow, epID))
			return nil
		case quality.UpgradeProperRepack:
			e.Accept(fmt.Sprintf("series: %s %s proper/repack accepted", matchedShow, epID))
			return nil
		}
		e.Reject(fmt.Sprintf("series: %s %s already downloaded", matchedShow, epID))
		return nil
	}

	if p.tracking == trackingStrict {
		if latest, ok := p.tracker.Latest(matchedShow); ok {
			if err := enforceStrict(tc.Logger, epID, latest); err != nil {
				e.Reject(err.Error())
				return nil
			}
		}
	}

	if p.tracking == trackingFollow {
		// On first encounter (no episodes tracked yet) accept everything —
		// handles binge dumps where a full season lands in a single run.
		// Once tracking is established, use the highest tracked episode as
		// the season floor: reject episodes from older seasons, accept
		// everything from the floor season onwards (including unseen episodes
		// within the floor season, e.g. mid-season gaps filled on a later run).
		// Using the highest episode (not earliest) prevents stale old-season
		// records from pulling the floor back to an earlier season.
		// For date-based shows fall back to comparing the full episode ID
		// string lexicographically.
		if highest, ok := p.tracker.HighestEpisode(matchedShow); ok {
			incomingSeason := e.GetInt(entry.FieldSeriesSeason)
			floorSeason := seasonFromEpisodeID(highest.EpisodeID)
			if incomingSeason > 0 && floorSeason > 0 {
				if incomingSeason < floorSeason {
					e.Reject(fmt.Sprintf("series: %s S%02d predates tracking window (at S%02d)",
						matchedShow, incomingSeason, floorSeason))
					return nil
				}
			} else if epID < highest.EpisodeID {
				e.Reject(fmt.Sprintf("series: %s %s predates tracking window (at %s)",
					matchedShow, epID, highest.EpisodeID))
				return nil
			}
		}
	}

	e.Accept(fmt.Sprintf("series: %s %s matched", matchedShow, epID))
	return nil
}

func (p *seriesPlugin) persist(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		// Only persist entries that were accepted by all downstream nodes.
		// The executor passes every entry the series node produced to Commit,
		// including those later rejected by dedup — we must filter them here
		// so the stored quality reflects the entry that was actually downloaded.
		if !e.IsAccepted() {
			continue
		}
		// matchedShow was stamped onto the entry by filter(); reading it back
		// here avoids re-resolving the show list at commit time.
		matchedShow := e.GetString(seriesTrackerName)
		if matchedShow == "" {
			continue
		}
		epID := e.GetString(entry.FieldSeriesEpisodeID)
		if epID == "" {
			continue
		}
		q, _ := e.Quality()
		rec := series.Record{
			SeriesName:   matchedShow,
			DisplayName:  e.GetString(entry.FieldTitle),
			EpisodeID:    epID,
			Quality:      q,
			DownloadedAt: time.Now(),
			Repack:       e.GetBool(entry.FieldVideoProper) || e.GetBool(entry.FieldVideoRepack),
		}
		// Build a minimal Episode for MarkWithParts so double-episode releases
		// also mark each individual part. MarkWithParts only consults Season,
		// Episode, and DoubleEpisode; date-based IDs (which can't be doubles)
		// naturally fall through with DoubleEpisode == 0.
		ep := &series.Episode{
			Season:        e.GetInt(entry.FieldSeriesSeason),
			Episode:       e.GetInt(entry.FieldSeriesEpisode),
			DoubleEpisode: e.GetInt(entry.FieldSeriesDoubleEpisode),
		}
		if err := p.tracker.MarkWithParts(rec, ep); err != nil {
			return fmt.Errorf("series: mark %s %s: %w", matchedShow, epID, err)
		}
	}
	return nil
}

func (p *seriesPlugin) resolveShows(ctx context.Context, tc *plugin.TaskContext) []match.TitleEntry {
	return plugin.ResolveDynamicList(ctx, tc, p.listSources, p.staticShows,
		func(src string) ([]match.TitleEntry, bool) { return p.listCache.Get(src) },
		func(src string, v []match.TitleEntry) { p.listCache.Set(src, v) },
	)
}

// matchShow returns the canonical show name if parsed matches any configured show.
// Series matching is title-only — shows air over multiple years so year
// comparison would cause false negatives for ongoing series.
func matchShow(parsed string, shows []match.TitleEntry) (string, bool) {
	norm := match.Normalize(parsed)
	for _, s := range shows {
		if match.Fuzzy(norm, s.Norm) {
			return s.Norm, true
		}
	}
	return "", false
}

// enforceStrict rejects episodes that skip more than one ahead of the latest
// downloaded episode (standard / absolute episode numbering only; date episodes
// skip this check because their IDs do not encode comparable season/episode
// numbers).
func enforceStrict(log *slog.Logger, epID string, latest *series.Record) error {
	incomingSeason, incomingEpisode, ok := series.ParseEpisodeID(epID)
	if !ok {
		return nil // date or unparseable: skip strict comparison
	}
	latestSeason, latestEpisode, ok := series.ParseEpisodeID(latest.EpisodeID)
	if !ok {
		log.Warn("series: strict tracking: stored episode ID did not parse, skipping strict check",
			"series", latest.SeriesName, "episode_id", latest.EpisodeID)
		return nil
	}
	if incomingSeason != latestSeason {
		return nil
	}
	gap := incomingEpisode - latestEpisode
	if gap > 1 {
		return fmt.Errorf("series: strict tracking: %s skips %d episodes ahead of latest %s",
			epID, gap-1, latest.EpisodeID)
	}
	return nil
}

// seasonFromEpisodeID extracts the season number from a zero-padded episode ID
// such as "S02E05" → 2. Returns 0 for date-based ("2023-11-15") or absolute
// ("EP123") IDs that carry no season number.
func seasonFromEpisodeID(epID string) int {
	if len(epID) >= 3 && (epID[0] == 'S' || epID[0] == 's') {
		var s int
		fmt.Sscanf(epID[1:], "%d", &s) //nolint:errcheck
		return s
	}
	return 0
}

func (p *seriesPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		// Series classifier: every entry that reaches this filter is a
		// series episode (Requires guarantees series_episode_id upstream).
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("series filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}

// Commit implements plugin.CommitPlugin. It persists episode tracking records
// for all entries that were accepted by Process and not subsequently failed by
// any downstream sink. This ensures we only mark episodes as downloaded when
// the full pipeline (including download/output) succeeded.
func (p *seriesPlugin) Commit(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	return p.persist(ctx, tc, entries)
}
