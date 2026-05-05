// Package series provides a TV series filter and learn plugin.
//
// It parses episode information from entry titles, matches them against a
// configured show list, and enforces quality and tracking constraints.
// Multiple quality variants of the same episode are all accepted so the
// task engine's automatic deduplication can choose the best copy. The
// tracker is updated in the Learn phase so only the dedup survivor is
// recorded as downloaded.
//
// The show list may be provided statically via 'shows', dynamically via 'from'
// (a list of input plugins whose entry titles are used as show names), or both.
// Dynamic lists are cached for the configured ttl (default: 1h).
package series

import (
	"context"
	"fmt"
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
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireOneOf(cfg, "series", "series", "shows", "from"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "ttl", "series"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "tracking", "series", "strict", "backfill", "all"); err != nil {
		errs = append(errs, err)
	}
	if q, _ := cfg["quality"].(string); q != "" {
		if _, err := quality.ParseSpec(q); err != nil {
			errs = append(errs, fmt.Errorf("series: invalid quality spec: %w", err))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "series", "shows", "from", "ttl", "tracking", "quality")...)
	return errs
}

// tracking controls how episode ordering is enforced.
type tracking string

const (
	trackingStrict   tracking = "strict"   // reject if episode number skips > 1 ahead of latest
	trackingBackfill tracking = "backfill" // accept any episode not yet downloaded
	trackingAll      tracking = "all"      // accept every episode regardless of tracking
)

type seriesPlugin struct {
	staticShows []string          // normalised show names from config
	from        []plugin.InputPlugin
	listCache   *cache.Cache[[]string]
	spec        quality.Spec
	tracking    tracking
	tracker     *series.Tracker
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	raw := toStringSlice(cfg["shows"])
	staticShows := make([]string, len(raw))
	for i, s := range raw {
		staticShows[i] = match.Normalize(s)
	}

	fromRaw, _ := cfg["from"].([]any)
	var froms []plugin.InputPlugin
	for _, item := range fromRaw {
		inp, err := plugin.MakeInputPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("series: from: %w", err)
		}
		froms = append(froms, inp)
	}

	if len(staticShows) == 0 && len(froms) == 0 {
		return nil, fmt.Errorf("series: at least one of 'shows' or 'from' is required")
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
		case trackingStrict, trackingBackfill, trackingAll:
			tr = tracking(t)
		default:
			return nil, fmt.Errorf("series: unknown tracking mode %q (strict|backfill|all)", t)
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

	return &seriesPlugin{
		staticShows: staticShows,
		from:        froms,
		listCache:   cache.NewPersistent[[]string](ttl, db.Bucket("cache_series_from")),
		spec:        spec,
		tracking:    tr,
		tracker:     tracker,
	}, nil
}

func (p *seriesPlugin) Name() string        { return "series" }
func (p *seriesPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *seriesPlugin) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	ep, ok := series.Parse(e.Title)
	if !ok {
		tc.Logger.Debug("series: title did not parse as episode", "entry", e.Title)
		return nil
	}

	shows := p.resolveShows(ctx, tc)
	matchedShow, ok := matchShow(ep.SeriesName, shows)
	if !ok {
		tc.Logger.Debug("series: no show match", "series", ep.SeriesName, "shows_loaded", len(shows))
		return nil
	}

	epID := series.EpisodeID(ep)
	e.Set("series_name", matchedShow)
	e.Set("series_episode_id", epID)
	e.Set("series_season", ep.Season)
	e.Set("series_episode", ep.Episode)

	if p.tracker.IsSeen(matchedShow, epID) {
		if ep.Proper || ep.Repack {
			if latest, ok := p.tracker.Latest(matchedShow); ok && latest.EpisodeID == epID {
				if ep.Quality.Better(latest.Quality) {
					e.Accept()
					return nil
				}
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
			if err := enforceStrict(ep, epID, latest); err != nil {
				e.Reject(err.Error())
				return nil
			}
		}
	}

	e.Accept()
	return nil
}

func (p *seriesPlugin) Learn(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	shows := p.resolveShows(ctx, tc)
	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
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
			SeriesName: matchedShow,
			EpisodeID:  epID,
			Quality:    ep.Quality,
		}); err != nil {
			return fmt.Errorf("series: mark %s %s: %w", matchedShow, epID, err)
		}
	}
	return nil
}

// resolveShows returns the full shows list: static + dynamically fetched.
// Dynamic results are cached for the configured TTL.
func (p *seriesPlugin) resolveShows(ctx context.Context, tc *plugin.TaskContext) []string {
	if len(p.from) == 0 {
		return p.staticShows
	}
	if dynamic, ok := p.listCache.Get("shows"); ok {
		tc.Logger.Debug("series: loaded shows from cache", "count", len(dynamic))
		return append(p.staticShows, dynamic...)
	}
	var dynamic []string
	innerTC := &plugin.TaskContext{Name: tc.Name, Logger: tc.Logger}
	for _, inp := range p.from {
		fromEntries, err := inp.Run(ctx, innerTC)
		if err != nil {
			tc.Logger.Warn("series: from source failed", "plugin", inp.Name(), "err", err)
			continue
		}
		tc.Logger.Debug("series: loaded shows from source", "plugin", inp.Name(), "count", len(fromEntries))
		for _, e := range fromEntries {
			if e.Title != "" {
				dynamic = append(dynamic, match.Normalize(e.Title))
			}
		}
	}
	p.listCache.Set("shows", dynamic)
	return append(p.staticShows, dynamic...)
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
func enforceStrict(ep *series.Episode, epID string, latest *series.Record) error {
	if ep.IsDate {
		return nil
	}
	latestEp, ok := series.Parse(latest.SeriesName + " " + latest.EpisodeID)
	if !ok {
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
