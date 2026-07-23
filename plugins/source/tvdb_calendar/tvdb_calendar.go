// Package tvdb_calendar emits one entry per upcoming episode of the shows in
// the series tracker, using TheTVDB episode air dates. Feed it into notify
// for a "tonight's episodes" digest, or into discover for day-of searching.
//
// Shows come from the shared series tracker (every show any pipeline has
// downloaded episodes for); shows marked inactive are skipped.
package tvdb_calendar

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

const pluginName = "tvdb_calendar"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "emit one entry per upcoming episode (within window) for shows in the series tracker",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTitle, entry.FieldMediaType, entry.FieldSeriesName,
			entry.FieldSeriesEpisodeID, entry.FieldSeriesSeason, entry.FieldSeriesEpisode,
			entry.FieldSeriesAirDate,
		},
		MayProduce: []string{"tvdb_id"},
		Factory:    newPlugin,
		Validate:   validate,
		Schema: []plugin.FieldSchema{
			{Key: "api_key", Type: plugin.FieldTypeString, Required: true, Hint: "TheTVDB v4 API key"},
			{Key: "window", Type: plugin.FieldTypeDuration, Default: "24h", Hint: "How far ahead to look for upcoming episodes"},
			{Key: "ttl", Type: plugin.FieldTypeDuration, Default: "6h", Hint: "TVDB lookup cache TTL"},
			{Key: "include_inactive", Type: plugin.FieldTypeBool, Hint: "Include shows deactivated by series_tracker_update"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptUnknownKeys(cfg, pluginName, "api_key", "window", "ttl", "include_inactive"); err != nil {
		errs = append(errs, err...)
	}
	if k, _ := cfg["api_key"].(string); k == "" {
		errs = append(errs, fmt.Errorf("%s: 'api_key' is required", pluginName))
	}
	if err := plugin.OptDuration(cfg, "window", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "ttl", pluginName); err != nil {
		errs = append(errs, err)
	}
	return errs
}

type calendarPlugin struct {
	resolver        *itvdb.Resolver
	tracker         *series.Tracker
	inactive        *series.InactiveSet
	window          time.Duration
	includeInactive bool
	now             func() time.Time
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("%s: 'api_key' is required", pluginName)
	}
	window := 24 * time.Hour
	if s, ok := cfg["window"].(string); ok && s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid window: %w", pluginName, err)
		}
		window = d
	}
	ttl := 6 * time.Hour
	if s, ok := cfg["ttl"].(string); ok && s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid ttl: %w", pluginName, err)
		}
		ttl = d
	}
	includeInactive, _ := cfg["include_inactive"].(bool)

	return &calendarPlugin{
		resolver: itvdb.NewResolver(itvdb.New(apiKey), ttl,
			db.Bucket("cache_tvdb_calendar"), db.Bucket("cache_tvdb_calendar_eps")),
		tracker:         series.NewTracker(db.Bucket(series.TrackerBucketName)),
		inactive:        series.NewInactiveSet(db.Bucket(series.InactiveBucketName)),
		window:          window,
		includeInactive: includeInactive,
		now:             time.Now,
	}, nil
}

func (p *calendarPlugin) Name() string { return pluginName }

// Generate implements plugin.SourcePlugin.
func (p *calendarPlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	shows, err := p.tracker.Summaries()
	if err != nil {
		return nil, fmt.Errorf("%s: read tracker: %w", pluginName, err)
	}
	now := p.now()
	horizon := now.Add(p.window)

	var out []*entry.Entry
	for _, show := range shows {
		if !p.includeInactive && p.inactive.IsInactive(show.Name) {
			continue
		}
		display := show.DisplayName
		if display == "" {
			display = show.Name
		}
		sr, err := p.resolver.ResolveSeries(ctx, "", display)
		if err != nil || sr == nil {
			// One unresolvable show must not kill the whole calendar.
			tc.Logger.Warn(pluginName+": series lookup failed", "show", display, "err", err)
			continue
		}
		eps, err := p.resolver.Episodes(ctx, sr.ID, display)
		if err != nil {
			tc.Logger.Warn(pluginName+": episodes lookup failed", "show", display, "err", err)
			continue
		}
		for i := range eps {
			ep := &eps[i]
			if ep.SeasonNumber == 0 || ep.AirDate == "" {
				continue
			}
			aired, err := time.Parse("2006-01-02", ep.AirDate)
			if err != nil {
				continue
			}
			if aired.Before(now.Truncate(24*time.Hour)) || aired.After(horizon) {
				continue
			}
			epID := fmt.Sprintf("S%02dE%02d", ep.SeasonNumber, ep.EpisodeNumber)
			e := entry.New(
				fmt.Sprintf("%s %s", display, epID),
				fmt.Sprintf("pipeliner://calendar/%s/%s", url.PathEscape(show.Name), epID),
			)
			e.Fields[entry.FieldMediaType] = "series"
			e.Fields[entry.FieldSeriesName] = show.Name
			e.Fields[entry.FieldSeriesEpisodeID] = epID
			e.Fields[entry.FieldSeriesSeason] = ep.SeasonNumber
			e.Fields[entry.FieldSeriesEpisode] = ep.EpisodeNumber
			e.Fields[entry.FieldSeriesAirDate] = aired
			if ep.Name != "" {
				e.Fields[entry.FieldDescription] = ep.Name
			}
			out = append(out, e)
		}
	}
	tc.Logger.Info(pluginName+": upcoming episodes", "count", len(out), "window", p.window.String())
	return out, nil
}
