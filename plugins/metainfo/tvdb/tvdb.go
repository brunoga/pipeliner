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
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "api_key", "metainfo_tvdb"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "cache_ttl", "metainfo_tvdb"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "metainfo_tvdb", "api_key", "cache_ttl")...)
	return errs
}

type tvdbPlugin struct {
	client        *itvdb.Client
	cache         *cache.Cache[[]itvdb.Series]
	extendedCache *cache.Cache[*itvdb.SeriesExtended]
	episodeCache  *cache.Cache[[]itvdb.Episode]
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

	p := &tvdbPlugin{
		client:        itvdb.New(apiKey),
		cache:         cache.NewPersistent[[]itvdb.Series](ttl, db.Bucket("cache_metainfo_tvdb")),
		extendedCache: cache.NewPersistent[*itvdb.SeriesExtended](ttl, db.Bucket("cache_metainfo_tvdb_ext")),
		episodeCache:  cache.NewPersistent[[]itvdb.Episode](ttl, db.Bucket("cache_metainfo_tvdb_eps")),
	}
	p.cache.Preload()
	p.extendedCache.Preload()
	p.episodeCache.Preload()
	return p, nil
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
		t0 := time.Now()
		var err error
		results, err = p.client.SearchSeries(ctx, ep.SeriesName)
		if err != nil {
			tc.Logger.Warn("metainfo_tvdb: search failed", "series", ep.SeriesName, "err", err)
			return nil
		}
		p.cache.Set(ep.SeriesName, results)
		tc.Logger.Debug("metainfo_tvdb: search", "series", ep.SeriesName, "duration", time.Since(t0).Round(time.Millisecond))
	} else {
		tc.Logger.Debug("metainfo_tvdb: search cache hit", "series", ep.SeriesName)
	}
	if len(results) == 0 {
		return nil
	}

	// Use the first result (highest relevance from TVDB).
	s := results[0]
	tc.Logger.Debug("metainfo_tvdb: search result",
		"series", ep.SeriesName,
		"id", s.ID,
		"network", s.Network,
		"language", s.Language,
		"genres", s.Genres,
		"image_url", s.ImageURL,
	)
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
	if s.Language != "" {
		e.Set("tvdb_language", languageName(s.Language))
	}
	if s.ImageURL != "" {
		e.Set("tvdb_poster", s.ImageURL)
	}
	if t := parseFirstAired(s.FirstAired); !t.IsZero() {
		e.Set("tvdb_first_air_date", t)
	}

	// Fetch extended data when the search result is missing genres or language,
	// which happens inconsistently across TVDB series.
	if s.ID != "" && (len(s.Genres) == 0 || s.Language == "" || s.FirstAired == "") {
		if ext, err := p.fetchExtended(ctx, tc, s.ID); err == nil {
			if len(s.Genres) == 0 {
				if names := ext.GenreNames(); len(names) > 0 {
					e.Set("tvdb_genres", names)
				}
			}
			if s.Language == "" && ext.Language != "" {
				e.Set("tvdb_language", languageName(ext.Language))
			}
			if s.FirstAired == "" {
				if t := parseFirstAired(ext.FirstAired); !t.IsZero() {
					e.Set("tvdb_first_air_date", t)
				}
			}
		}
	}

	// Fetch episode-level detail if we have a specific episode.
	if ep.Season > 0 && ep.Episode > 0 {
		eps, err := p.fetchEpisodes(ctx, tc, s.ID)
		if err != nil {
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

func (p *tvdbPlugin) fetchEpisodes(ctx context.Context, tc *plugin.TaskContext, id string) ([]itvdb.Episode, error) {
	if eps, ok := p.episodeCache.Get(id); ok {
		tc.Logger.Debug("metainfo_tvdb: episodes cache hit", "id", id)
		return eps, nil
	}
	t0 := time.Now()
	eps, err := p.client.GetEpisodes(ctx, id)
	if err != nil {
		tc.Logger.Warn("metainfo_tvdb: episodes fetch failed", "id", id, "err", err)
		return nil, err
	}
	p.episodeCache.Set(id, eps)
	tc.Logger.Debug("metainfo_tvdb: episodes fetch", "id", id, "count", len(eps), "duration", time.Since(t0).Round(time.Millisecond))
	return eps, nil
}

func (p *tvdbPlugin) fetchExtended(ctx context.Context, tc *plugin.TaskContext, id string) (*itvdb.SeriesExtended, error) {
	if ext, ok := p.extendedCache.Get(id); ok {
		tc.Logger.Debug("metainfo_tvdb: extended cache hit", "id", id)
		return ext, nil
	}
	t0 := time.Now()
	ext, err := p.client.GetSeriesExtended(ctx, id)
	if err != nil {
		tc.Logger.Warn("metainfo_tvdb: extended fetch failed", "id", id, "err", err)
		return nil, err
	}
	p.extendedCache.Set(id, ext)
	tc.Logger.Debug("metainfo_tvdb: extended fetch", "id", id, "duration", time.Since(t0).Round(time.Millisecond))
	return ext, nil
}

// languageName maps ISO 639-2 three-letter codes to English display names.
// Falls back to the original code when not found.
func languageName(code string) string {
	if name, ok := iso639[code]; ok {
		return name
	}
	return code
}

var iso639 = map[string]string{
	"ara": "Arabic",
	"bul": "Bulgarian",
	"ces": "Czech",
	"chi": "Chinese",
	"zho": "Chinese",
	"hrv": "Croatian",
	"dan": "Danish",
	"nld": "Dutch",
	"eng": "English",
	"fin": "Finnish",
	"fra": "French",
	"deu": "German",
	"ger": "German",
	"ell": "Greek",
	"heb": "Hebrew",
	"hin": "Hindi",
	"hun": "Hungarian",
	"ind": "Indonesian",
	"ita": "Italian",
	"jpn": "Japanese",
	"kor": "Korean",
	"msa": "Malay",
	"nor": "Norwegian",
	"pol": "Polish",
	"por": "Portuguese",
	"ron": "Romanian",
	"rum": "Romanian",
	"rus": "Russian",
	"slk": "Slovak",
	"slo": "Slovak",
	"spa": "Spanish",
	"swe": "Swedish",
	"tha": "Thai",
	"tur": "Turkish",
	"ukr": "Ukrainian",
	"vie": "Vietnamese",
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
