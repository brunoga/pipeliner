// Package lifecycle provides the series_lifecycle processor, which classifies
// tracked shows by follow lifecycle using TheTVDB status and episode list:
//
//	complete - status Ended/Cancelled AND every aired episode is in the tracker
//	dormant  - status Ended/Cancelled but aired episodes are missing (backfill)
//	active   - anything else, including lookup failures (keep following)
//
// Upstream entries must carry series_name (the normalized tracker key, as
// emitted by the series_tracker source) or at least a title. TVDB lookups are
// cached in store buckets with a configurable TTL, like other metadata
// plugins.
//
// Aired-episode comparison counts only episodes whose air date is in the
// past. Specials (season 0) are excluded unless include_specials=true.
// Date-numbered shows (tracker IDs like 2023-11-15) cannot be matched against
// TVDB's season/episode numbering, so an ended date-numbered show classifies
// as dormant rather than complete.
//
// Config keys:
//
//	api_key          - TheTVDB API key (required)
//	cache_ttl        - how long to cache TVDB lookups (default: "24h")
//	include_specials - count season-0 episodes as aired (default: false)
package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

const pluginName = "series_lifecycle"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "classify tracked shows as complete/dormant/active from TheTVDB status + episode list vs the series tracker",
		Role:        plugin.RoleProcessor,
		// series_name is the tracker key; title is accepted as a fallback
		// (normalized on the fly) so entries from generic list sources work.
		Requires: plugin.RequireAny(entry.FieldSeriesName, entry.FieldTitle),
		// Every entry is classified: lookup failures default to "active"
		// (keep following — never deactivate a show on missing data).
		Produces: []string{entry.FieldSeriesLifecycle},
		// Set only when the TVDB lookup (and, for ended shows, the episode
		// list fetch) succeeds.
		MayProduce: []string{
			entry.FieldSeriesStatus,
			entry.FieldSeriesAiredEpisodeCount,
			entry.FieldSeriesMissingEpisodeCount,
			"tvdb_id",
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "api_key", Type: plugin.FieldTypeString, Required: true, Hint: "TheTVDB v4 API key"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "24h", Hint: "How long to cache TVDB lookups"},
			{Key: "include_specials", Type: plugin.FieldTypeBool, Default: false, Hint: "Count season-0 specials as aired episodes"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_series_lifecycle", Display: "Series Lifecycle Search Cache"},
			{Name: "cache_series_lifecycle_eps", Display: "Series Lifecycle Episodes Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "api_key", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "cache_ttl", pluginName); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, "api_key", "cache_ttl", "include_specials")...)
	return errs
}

type lifecyclePlugin struct {
	resolver        *itvdb.Resolver
	tracker         *series.Tracker
	includeSpecials bool
	// now is the reference time for "already aired"; overridable in tests.
	now func() time.Time
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("%s: 'api_key' is required", pluginName)
	}

	ttl := 24 * time.Hour
	if v, _ := cfg["cache_ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid cache_ttl %q: %w", pluginName, v, err)
		}
		ttl = d
	}

	return &lifecyclePlugin{
		resolver: itvdb.NewResolver(itvdb.New(apiKey), ttl,
			db.Bucket("cache_series_lifecycle"), db.Bucket("cache_series_lifecycle_eps")),
		tracker:         series.NewTracker(db.Bucket(series.TrackerBucketName)),
		includeSpecials: plugin.OptBool(cfg, "include_specials", false),
		now:             time.Now,
	}, nil
}

func (p *lifecyclePlugin) Name() string { return pluginName }

func (p *lifecyclePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		p.classify(ctx, tc, e)
	}
	return entries, nil
}

// classify resolves the entry's show on TVDB and stamps series_lifecycle
// (and, when available, series_status / tvdb_id / episode counts).
func (p *lifecyclePlugin) classify(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) {
	// Display name for the TVDB search; normalized name for the tracker key.
	searchName := e.GetString(entry.FieldTitle)
	trackerName := e.GetString(entry.FieldSeriesName)
	if searchName == "" {
		searchName = trackerName
	}
	if trackerName == "" {
		trackerName = match.Normalize(searchName)
	}
	if searchName == "" {
		tc.Logger.Warn(pluginName + ": entry has neither series_name nor title")
		e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleActive)
		return
	}

	s, err := p.resolver.ResolveSeries(ctx, e.GetString("tvdb_id"), searchName)
	if err != nil {
		tc.Logger.Warn(pluginName+": TVDB lookup failed; classifying as active",
			"series", searchName, "err", err)
		e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleActive)
		return
	}
	if s == nil {
		tc.Logger.Warn(pluginName+": TVDB lookup found no match; classifying as active", "series", searchName)
		e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleActive)
		return
	}

	if s.ID != "" {
		e.Set("tvdb_id", s.ID)
	}
	status := string(s.Status)
	if status != "" {
		e.Set(entry.FieldSeriesStatus, status)
	}

	if !isEnded(status) {
		e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleActive)
		return
	}

	eps, err := p.resolver.Episodes(ctx, s.ID, searchName)
	if err != nil {
		// Ended but unverifiable: stay active rather than emitting a wrong
		// "dormant" (would trigger backfill flows) or "complete" (would
		// deactivate the show).
		tc.Logger.Warn(pluginName+": episode list unavailable; classifying as active",
			"series", searchName, "err", err)
		e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleActive)
		return
	}

	aired, missing := p.diffAired(eps, trackerName)
	e.Set(entry.FieldSeriesAiredEpisodeCount, aired)
	e.Set(entry.FieldSeriesMissingEpisodeCount, missing)
	if missing == 0 {
		e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleComplete)
	} else {
		e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleDormant)
	}
}

// diffAired counts episodes that have already aired and how many of them are
// absent from the series tracker.
func (p *lifecyclePlugin) diffAired(eps []itvdb.Episode, trackerName string) (aired, missing int) {
	now := p.now()
	for i := range eps {
		ep := &eps[i]
		if !itvdb.EpisodeAired(ep, now, p.includeSpecials) {
			continue
		}
		aired++
		epID := series.EpisodeID(&series.Episode{Season: ep.SeasonNumber, Episode: ep.EpisodeNumber})
		if !p.tracker.IsSeen(trackerName, epID) {
			missing++
		}
	}
	return aired, missing
}

// isEnded reports whether a series status string means the show is over.
// TheTVDB uses capitalised values ("Ended", "Cancelled"); Trakt-sourced
// entries use lowercase ("ended", "canceled") — compare case-insensitively
// and accept both spellings of cancelled.
func isEnded(status string) bool {
	switch strings.ToLower(status) {
	case "ended", "cancelled", "canceled":
		return true
	}
	return false
}
