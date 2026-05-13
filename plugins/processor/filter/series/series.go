// Package series provides a TV series processor that accepts episodes from a
// configured show list and tracks downloads across runs.
//
// It parses episode information from entry titles, matches against the show
// list, and enforces quality and tracking constraints. Multiple quality
// variants of the same episode are accepted so the dedup processor can choose
// the best copy. State is persisted within Process() so the best-quality
// episode is recorded after dedup selects it.
//
// The show list may be provided statically via 'static', dynamically via 'from'
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
		Produces: []string{
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			entry.FieldSeriesEpisodeID,
		},
		Factory:     newPlugin,
		Validate:    validate,
		AcceptsList: true,
		Schema: []plugin.FieldSchema{
			{Key: "static", Type: plugin.FieldTypeList, Hint: "Static list of show names to accept"},
			{Key: "list", Type: plugin.FieldTypeDict, Hint: "Dynamic show list from a source plugin (e.g. tvdb_favorites, trakt_list)"},
			{Key: "tracking", Type: plugin.FieldTypeEnum, Enum: []string{"strict", "backfill", "follow"}, Default: "strict", Hint: "Episode ordering mode"},
			{Key: "quality", Type: plugin.FieldTypeString, Hint: "Minimum quality spec, e.g. 720p+ webrip+"},
			{Key: "ttl", Type: plugin.FieldTypeDuration, Default: "1h", Hint: "Cache TTL for dynamic lists"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject episodes not in the show list"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireOneOf(cfg, "series", "static", "list"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "ttl", "series"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "tracking", "series", "strict", "backfill", "follow"); err != nil {
		errs = append(errs, err)
	}
	if q, _ := cfg["quality"].(string); q != "" {
		if _, err := quality.ParseSpec(q); err != nil {
			errs = append(errs, fmt.Errorf("series: invalid quality spec: %w", err))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "series", "static", "list", "ttl", "tracking", "quality", "reject_unmatched")...)
	return errs
}

// tracking controls how episode ordering is enforced.
type tracking string

const (
	trackingStrict   tracking = "strict"   // reject if episode number skips > 1 ahead of latest
	trackingBackfill tracking = "backfill" // accept any episode not yet downloaded
	trackingFollow   tracking = "follow"   // accept all on first encounter; thereafter reject episodes older than the earliest ever downloaded
)

type seriesPlugin struct {
	staticShows     []string // normalised show names from config
	from            []plugin.SourcePlugin
	listCache       *cache.Cache[[]string]
	spec            quality.Spec
	tracking        tracking
	tracker         *series.Tracker
	rejectUnmatched bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	raw := toStringSlice(cfg["static"])
	staticShows := make([]string, len(raw))
	for i, s := range raw {
		staticShows[i] = match.Normalize(s)
	}

	listRaw, _ := cfg["list"].([]any)
	var froms []plugin.SourcePlugin
	for _, item := range listRaw {
		src, err := plugin.MakeFromPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("series: list: %w", err)
		}
		froms = append(froms, src)
	}

	if len(staticShows) == 0 && len(froms) == 0 {
		return nil, fmt.Errorf("series: at least one of 'static' or 'list' is required")
	}

	ttl := time.Hour
	if v, _ := cfg["ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("series: invalid ttl %q: %w", v, err)
		}
		ttl = d
	}

	tracker := series.NewTracker(db.Bucket("series"))

	tr := trackingStrict
	if t, _ := cfg["tracking"].(string); t != "" {
		switch tracking(t) {
		case trackingStrict, trackingBackfill, trackingFollow:
			tr = tracking(t)
		default:
			return nil, fmt.Errorf("series: unknown tracking mode %q (strict|backfill|follow)", t)
		}
	}

	var spec quality.Spec
	if q, _ := cfg["quality"].(string); q != "" {
		s, err := quality.ParseSpec(q)
		if err != nil {
			return nil, fmt.Errorf("series: invalid quality spec: %w", err)
		}
		spec.MinResolution = s.MinResolution
		spec.MinSource = s.MinSource
		spec.MinCodec = s.MinCodec
		spec.MinAudio = s.MinAudio
		spec.MinColorRange = s.MinColorRange
	}

	rejectUnmatched := true
	if v, ok := cfg["reject_unmatched"]; ok {
		rejectUnmatched, _ = v.(bool)
	}

	return &seriesPlugin{
		staticShows:     staticShows,
		from:            froms,
		listCache:       cache.NewPersistent[[]string](ttl, db.Bucket("cache_series_from")),
		spec:            spec,
		tracking:        tr,
		tracker:         tracker,
		rejectUnmatched: rejectUnmatched,
	}, nil
}

func (p *seriesPlugin) Name() string        { return "series" }

func (p *seriesPlugin) filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	ep, ok := series.Parse(e.Title)
	if !ok {
		if p.rejectUnmatched {
			e.Reject("series: title did not parse as episode")
		}
		return nil
	}

	shows := p.resolveShows(ctx, tc)
	matchedShow, ok := matchShow(ep.SeriesName, shows)
	if !ok {
		if p.rejectUnmatched {
			e.Reject("series: show not in list")
		}
		return nil
	}

	epID := series.EpisodeID(ep)
	e.SetSeriesInfo(entry.SeriesInfo{
		Season:    ep.Season,
		Episode:   ep.Episode,
		EpisodeID: epID,
	})

	if p.tracker.IsSeen(matchedShow, epID) {
		if latest, ok := p.tracker.Latest(matchedShow); ok && latest.EpisodeID == epID {
			betterQuality := ep.Quality.Better(latest.Quality)
			properOrRepack := ep.Proper || ep.Repack
			notDowngrade := !latest.Quality.Better(ep.Quality)
			if betterQuality || (properOrRepack && notDowngrade) {
				e.Accept()
				return nil
			}
		}
		e.Reject(fmt.Sprintf("series: %s %s already downloaded", matchedShow, epID))
		return nil
	}

	if (p.spec != quality.Spec{}) && !p.spec.Matches(ep.Quality) {
		e.Reject(fmt.Sprintf("series: %s %s quality %s does not match spec",
			matchedShow, epID, ep.Quality.String()))
		return nil
	}

	if p.tracking == trackingStrict {
		if latest, ok := p.tracker.Latest(matchedShow); ok {
			if err := enforceStrict(tc.Logger, ep, epID, latest); err != nil {
				e.Reject(err.Error())
				return nil
			}
		}
	}

	if p.tracking == trackingFollow {
		// On first encounter (no episodes tracked yet) accept everything —
		// handles binge dumps where a full season lands in a single run.
		// Once tracking is established, use the earliest tracked season as
		// the anchor: reject episodes from older seasons, accept everything
		// from the anchor season onwards (including unseen episodes of the
		// anchor season itself, e.g. mid-season gaps filled on a later run).
		// For date-based shows (no season number) fall back to comparing
		// the full episode ID string lexicographically.
		if earliest, ok := p.tracker.Earliest(matchedShow); ok {
			anchorSeason := seasonFromEpisodeID(earliest.EpisodeID)
			if ep.Season > 0 && anchorSeason > 0 {
				if ep.Season < anchorSeason {
					e.Reject(fmt.Sprintf("series: %s S%02d predates tracking start (S%02d)",
						matchedShow, ep.Season, anchorSeason))
					return nil
				}
			} else if epID < earliest.EpisodeID {
				e.Reject(fmt.Sprintf("series: %s %s predates tracking start (%s)",
					matchedShow, epID, earliest.EpisodeID))
				return nil
			}
		}
	}

	e.Accept()
	return nil
}

func (p *seriesPlugin) persist(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	shows := p.resolveShows(ctx, tc)
	for _, e := range entries {
		ep, ok := series.Parse(e.Title)
		if !ok {
			continue
		}
		matchedShow, ok := matchShow(ep.SeriesName, shows)
		if !ok {
			continue
		}
		epID := series.EpisodeID(ep)
		if err := p.tracker.Mark(series.Record{
			SeriesName:  matchedShow,
			DisplayName: e.GetString(entry.FieldTitle),
			EpisodeID:   epID,
			Quality:     ep.Quality,
		}); err != nil {
			return fmt.Errorf("series: mark %s %s: %w", matchedShow, epID, err)
		}
	}
	return nil
}

func (p *seriesPlugin) resolveShows(ctx context.Context, tc *plugin.TaskContext) []string {
	return plugin.ResolveDynamicList(ctx, tc, p.from, p.staticShows,
		func(src string) ([]string, bool) { return p.listCache.Get(src) },
		func(src string, v []string) { p.listCache.Set(src, v) },
		match.Normalize,
	)
}

// matchShow returns the canonical show name if parsed matches any configured show.
func matchShow(parsed string, shows []string) (string, bool) {
	norm := match.Normalize(parsed)
	for _, show := range shows {
		if match.Fuzzy(norm, show) {
			return show, true
		}
	}
	return "", false
}

// enforceStrict rejects episodes that skip more than one ahead of the latest
// downloaded episode (standard episode numbering only; date episodes skip this check).
func enforceStrict(log *slog.Logger, ep *series.Episode, epID string, latest *series.Record) error {
	if ep.IsDate {
		return nil
	}
	latestEp, ok := series.Parse(latest.SeriesName + " " + latest.EpisodeID)
	if !ok {
		log.Warn("series: strict tracking: stored episode ID did not parse, skipping strict check",
			"series", latest.SeriesName, "episode_id", latest.EpisodeID)
		return nil
	}
	if ep.Season != latestEp.Season {
		return nil
	}
	gap := ep.Episode - latestEp.Episode
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

func (p *seriesPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
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
