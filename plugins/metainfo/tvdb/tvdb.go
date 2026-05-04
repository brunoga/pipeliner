// Package tvdb provides a metainfo plugin that enriches series entries with TheTVDB data.
//
// Config keys:
//
//	api_key   - TheTVDB API key (required)
//	cache_ttl - how long to cache search results, e.g. "24h" (default: "24h")
package tvdb

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_tvdb",
		Description: "enrich series entries with TheTVDB metadata (title, air date, overview)",
		PluginPhase: plugin.PhaseMetainfo,
		Factory:     newPlugin,
	})
}

type tvdbPlugin struct {
	client *itvdb.Client
	cache  *cache.Cache[[]itvdb.Series]
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("metainfo_tvdb: 'api_key' is required")
	}

	ttl := 24 * time.Hour
	if v, _ := cfg["cache_ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("metainfo_tvdb: invalid cache_ttl %q: %w", v, err)
		}
		ttl = d
	}

	return &tvdbPlugin{
		client: itvdb.New(apiKey),
		cache:  cache.NewPersistent[[]itvdb.Series](ttl, db.Bucket("cache_metainfo_tvdb")),
	}, nil
}

func (p *tvdbPlugin) Name() string        { return "metainfo_tvdb" }
func (p *tvdbPlugin) Phase() plugin.Phase { return plugin.PhaseMetainfo }

func (p *tvdbPlugin) Annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	ep, ok := series.Parse(e.Title)
	if !ok {
		return nil
	}

	results, cached := p.cache.Get(ep.SeriesName)
	if !cached {
		var err error
		results, err = p.client.SearchSeries(ctx, ep.SeriesName)
		if err != nil {
			tc.Logger.Warn("metainfo_tvdb: search failed", "series", ep.SeriesName, "err", err)
			return nil
		}
		p.cache.Set(ep.SeriesName, results)
	}
	if len(results) == 0 {
		return nil
	}

	// Use the first result (highest relevance from TVDB).
	s := results[0]
	e.Set("tvdb_id", s.ID)
	e.Set("tvdb_series_name", s.Name)
	e.Set("tvdb_series_year", s.Year)
	e.Set("tvdb_overview", s.Overview)
	e.Set("tvdb_slug", s.Slug)
	if len(s.Genres) > 0 {
		e.Set("tvdb_genres", s.Genres)
	}
	if s.Network != "" {
		e.Set("tvdb_network", s.Network)
	}
	if t := parseFirstAired(s.FirstAired); !t.IsZero() {
		e.Set("tvdb_first_air_date", t)
	}

	// Fetch episode-level detail if we have a specific episode.
	if ep.Season > 0 && ep.Episode > 0 {
		eps, err := p.client.GetEpisodes(ctx, s.ID)
		if err != nil {
			tc.Logger.Warn("metainfo_tvdb: episodes fetch failed", "id", s.ID, "err", err)
			return nil
		}
		for _, ep2 := range eps {
			if ep2.SeasonNumber == ep.Season && ep2.EpisodeNumber == ep.Episode {
				e.Set("tvdb_episode_id", ep2.ID)
				e.Set("tvdb_episode_name", ep2.Name)
				e.Set("tvdb_air_date", ep2.AirDate)
				e.Set("tvdb_episode_overview", ep2.Overview)
				break
			}
		}
	}

	return nil
}

// parseFirstAired parses the first-air-time string returned by the TVDB search
// API. The format varies: ISO-8601 with time ("2008-01-20T05:00:00.000Z") or
// plain date ("2008-01-20"). Returns zero time on failure.
func parseFirstAired(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
