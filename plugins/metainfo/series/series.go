// Package series provides a metainfo plugin that annotates entries with episode metadata.
package series

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_series",
		Description: "parse series/episode info from entry title and annotate fields",
		PluginPhase: plugin.PhaseMetainfo,
		Factory:     newPlugin,
	})
}

type seriesMetaPlugin struct{}

func newPlugin(_ map[string]any) (plugin.Plugin, error) {
	return &seriesMetaPlugin{}, nil
}

func (p *seriesMetaPlugin) Name() string        { return "metainfo_series" }
func (p *seriesMetaPlugin) Phase() plugin.Phase { return plugin.PhaseMetainfo }

func (p *seriesMetaPlugin) Annotate(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	ep, ok := series.Parse(e.Title)
	if !ok {
		return nil
	}
	e.Set("series_name", ep.SeriesName)
	e.Set("series_season", ep.Season)
	e.Set("series_episode", ep.Episode)
	e.Set("series_id", series.EpisodeID(ep))
	e.Set("series_proper", ep.Proper)
	e.Set("series_repack", ep.Repack)
	if ep.Service != "" {
		e.Set("series_service", ep.Service)
	}
	if ep.Container != "" {
		e.Set("series_container", ep.Container)
	}
	if ep.IsDate {
		e.Set("series_date", fmt.Sprintf("%04d-%02d-%02d", ep.Year, ep.Month, ep.Day))
	}
	if ep.DoubleEpisode > 0 {
		e.Set("series_double_episode", ep.DoubleEpisode)
	}
	return nil
}
