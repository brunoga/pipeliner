// Package series provides a metainfo plugin that annotates entries with episode metadata.
package series

import (
	"context"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/series"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_series",
		Description: "parse series/episode info from entry title and annotate fields",
		PluginPhase: plugin.PhaseMetainfo,
		Role:        plugin.RoleProcessor,
		Produces: []string{
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			entry.FieldSeriesEpisodeID,
			entry.FieldSeriesProper,
			entry.FieldSeriesRepack,
			entry.FieldSeriesDoubleEpisode,
			entry.FieldSeriesService,
		},
		Factory: newPlugin,
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "metainfo_series")
		},
	})
}

type seriesMetaPlugin struct{}

func newPlugin(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	return &seriesMetaPlugin{}, nil
}

func (p *seriesMetaPlugin) Name() string        { return "metainfo_series" }
func (p *seriesMetaPlugin) Phase() plugin.Phase { return plugin.PhaseMetainfo }

func (p *seriesMetaPlugin) Annotate(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	ep, ok := series.Parse(e.Title)
	if !ok {
		return nil
	}
	epID := series.EpisodeID(ep)
	if ep.Container != "" {
		e.Set("series_container", ep.Container)
	}
	e.SetSeriesInfo(entry.SeriesInfo{
		VideoInfo:     entry.VideoInfo{GenericInfo: entry.GenericInfo{Title: ep.SeriesName}},
		Season:        ep.Season,
		Episode:       ep.Episode,
		EpisodeID:     epID,
		Proper:        ep.Proper,
		Repack:        ep.Repack,
		Service:       ep.Service,
		DoubleEpisode: ep.DoubleEpisode,
	})
	return nil
}

func (p *seriesMetaPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.Annotate(ctx, tc, e); err != nil {
			tc.Logger.Warn("metainfo_series error", "entry", e.Title, "err", err)
		}
	}
	return entries, nil
}
