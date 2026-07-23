// Package series_tracker provides a source plugin that emits one entry per
// show tracked by the series tracker — the same store the series and premiere
// filters write their per-episode download records to.
//
// The tracker bucket is deliberately not namespaced by task (see
// series.TrackerBucketName), so this source sees every show tracked by any
// pipeline without extra configuration. Feed it into series_lifecycle to
// classify shows as complete/dormant/active, or use it as a list= source.
//
// Entry shape:
//
//	Title                      display name (falls back to the normalized name)
//	URL                        pipeliner://series/<normalized-name> (stable, for dedup)
//	series_name                normalized tracker key
//	series_episode_count       number of downloaded episodes on record
//	series_newest_episode      highest tracked episode ID (e.g. S03E08)
//	series_last_downloaded_at  most recent download timestamp
//	series_inactive            true when the show has been deactivated
package series_tracker

import (
	"context"
	"net/url"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "series_tracker"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "emit one entry per show tracked by the series tracker; usable as a standalone DAG source or inside list=",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTitle,
			entry.FieldSource,
			entry.FieldMediaType,
			entry.FieldSeriesName,
			entry.FieldSeriesEpisodeCount,
			entry.FieldSeriesNewestEpisode,
			entry.FieldSeriesInactive,
		},
		// LastDownloadedAt is zero (and therefore unset) only for legacy
		// records written before DownloadedAt existed; treat it as conditional.
		MayProduce: []string{
			entry.FieldSeriesLastDownloadedAt,
		},
		Factory:      newPlugin,
		Validate:     validate,
		IsListPlugin: true,
	})
}

func validate(cfg map[string]any) []error {
	return plugin.OptUnknownKeys(cfg, pluginName)
}

type trackerSourcePlugin struct {
	tracker  *series.Tracker
	inactive *series.InactiveSet
}

func newPlugin(_ map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	return &trackerSourcePlugin{
		tracker:  series.NewTracker(db.Bucket(series.TrackerBucketName)),
		inactive: series.NewInactiveSet(db.Bucket(series.InactiveBucketName)),
	}, nil
}

func (p *trackerSourcePlugin) Name() string { return pluginName }

func (p *trackerSourcePlugin) Generate(_ context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	summaries, err := p.tracker.Summaries()
	if err != nil {
		return nil, err
	}

	entries := make([]*entry.Entry, 0, len(summaries))
	for _, s := range summaries {
		title := s.DisplayName
		if title == "" {
			title = s.Name
		}
		// Synthetic stable URL so dedup and cross-branch matching by URL work.
		u := "pipeliner://series/" + url.PathEscape(s.Name)
		e := entry.New(title, u)
		e.Set(entry.FieldSource, pluginName+":tracker")
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		e.Set(entry.FieldSeriesName, s.Name)
		e.Set(entry.FieldSeriesEpisodeCount, s.EpisodeCount)
		e.Set(entry.FieldSeriesNewestEpisode, s.NewestEpisodeID)
		if !s.LastDownloadedAt.IsZero() {
			e.Set(entry.FieldSeriesLastDownloadedAt, s.LastDownloadedAt)
		}
		e.Set(entry.FieldSeriesInactive, p.inactive.IsInactive(s.Name))
		entries = append(entries, e)
	}
	tc.Logger.Debug(pluginName+": generated tracked shows", "count", len(entries))
	return entries, nil
}
