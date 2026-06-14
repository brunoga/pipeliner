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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

// reTrailingYearParen matches a series name ending with a parenthesized year —
// unambiguously a production year: "Show (2019)" or "Show(2019)".
var reTrailingYearParen = regexp.MustCompile(`^(.*\S)\s*\(((?:19|20)\d{2})\)$`)

// reTrailingYearBare matches a series name ending with a bare year — ambiguous
// since the year might be part of the title: "Dark 2017" but also "Class of 1984".
var reTrailingYearBare = regexp.MustCompile(`^(.*\S)\s+((?:19|20)\d{2})$`)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_tvdb",
		Description: "enrich series entries with TheTVDB metadata (title, air date, overview, popularity)",
		Role:        plugin.RoleProcessor,
		MayProduce: []string{
			entry.FieldEnriched,
			entry.FieldTitle,
			entry.FieldMediaType,
			entry.FieldDescription,
			entry.FieldVideoYear,
			entry.FieldVideoLanguage,
			entry.FieldVideoOriginalTitle,
			entry.FieldVideoCountry,
			entry.FieldVideoGenres,
			entry.FieldVideoPopularity,
			entry.FieldVideoPoster,
			entry.FieldVideoCast,
			entry.FieldVideoContentRating,
			entry.FieldVideoRuntime,
			entry.FieldVideoTrailers,
			entry.FieldVideoAliases,
			entry.FieldSeriesNetwork,
			entry.FieldSeriesStatus,
			entry.FieldSeriesFirstAirDate,
			entry.FieldSeriesLastAirDate,
			entry.FieldSeriesNextAirDate,
			entry.FieldSeriesEpisodeID,
			entry.FieldSeriesEpisodeTitle,
			entry.FieldSeriesEpisodeDescription,
			entry.FieldSeriesEpisodeAirDate,
			entry.FieldSeriesEpisodeImage,
			"tvdb_id",
			"tvdb_slug",
			"tvdb_episode_id",
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "api_key", Type: plugin.FieldTypeString, Required: true, Hint: "TheTVDB v4 API key"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "24h", Hint: "How long to cache results"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_metainfo_tvdb", Display: "TVDB Search Cache"},
			{Name: "cache_metainfo_tvdb_ext", Display: "TVDB Extended Cache"},
			{Name: "cache_metainfo_tvdb_eps", Display: "TVDB Episodes Cache"},
		},
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

// cachedEpisodes wraps the per-series episode list with the series name so the
// database tab can label each row with something more useful than the raw
// numeric series ID. The JSON shape (lowercase "name", lowercase "episodes")
// is what the UI's cacheKeyTitle and cacheValuePreview helpers expect.
//
// Old cache entries written before this wrapper existed stored a bare JSON
// array; they will fail to unmarshal into this struct and be treated as
// cache misses, triggering a transparent refetch on first access.
type cachedEpisodes struct {
	Name     string          `json:"name"`
	Episodes []itvdb.Episode `json:"episodes"`
}

type tvdbPlugin struct {
	client        *itvdb.Client
	cache         *cache.Cache[[]itvdb.Series]
	extendedCache *cache.Cache[*itvdb.SeriesExtended]
	episodeCache  *cache.Cache[cachedEpisodes]
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
		episodeCache:  cache.NewPersistent[cachedEpisodes](ttl, db.Bucket("cache_metainfo_tvdb_eps")),
	}
	p.cache.Preload()
	p.extendedCache.Preload()
	p.episodeCache.Preload()
	return p, nil
}

func (p *tvdbPlugin) Name() string { return "metainfo_tvdb" }

func (p *tvdbPlugin) annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	ep, ok := series.Parse(e.Title)
	if !ok {
		tc.Logger.Warn("metainfo_tvdb: title did not parse as series episode", "entry", e.Title)
		return nil
	}

	results := p.searchSeries(ctx, tc, ep.SeriesName)
	if len(results) == 0 {
		tc.Logger.Warn("metainfo_tvdb: no results", "series", ep.SeriesName, "entry", e.Title)
		return nil
	}

	// Use the first result (highest relevance from TVDB).
	s := results[0]
	tc.Logger.Debug("metainfo_tvdb: search result", "series", ep.SeriesName, "id", s.ID, "name", s.Name)

	e.Set("tvdb_id", s.ID)
	if s.Slug != "" {
		e.Set("tvdb_slug", s.Slug)
	}

	// Build the standard SeriesInfo. Extended data is authoritative; search is
	// the fallback for fields the extended endpoint may not return.
	// Enriched is always true — TVDB found the show.
	si := entry.SeriesInfo{}
	si.Enriched = true
	if s.ID != "" {
		if ext, err := p.fetchExtended(ctx, tc, s.ID); err == nil {
			if ext.Slug != "" {
				e.Set("tvdb_slug", ext.Slug)
			}
			si.Title = ext.Name
			if si.Title == "" {
				si.Title = s.Name
			}
			si.Description = ext.Overview
			if si.Description == "" {
				si.Description = s.Overview
			}
			si.Poster = ext.Image
			if si.Poster == "" {
				si.Poster = s.ImageURL
			}
			if names := ext.GenreNames(); len(names) > 0 {
				si.Genres = names
			}
			si.Network = ext.OriginalNetwork.Name
			if si.Network == "" {
				si.Network = s.Network
			}
			lang := ext.Language
			if lang == "" {
				lang = s.Language
			}
			si.Language = itvdb.LanguageName(lang)
			country := ext.OriginalCountry
			if country == "" {
				country = s.Country
			}
			si.Country = itvdb.CountryName(country)
			if t := itvdb.ParseDate(ext.FirstAired); !t.IsZero() {
				si.FirstAirDate = t
				si.PublishedDate = ext.FirstAired
			} else if t := itvdb.ParseDate(s.FirstAired); !t.IsZero() {
				si.FirstAirDate = t
				si.PublishedDate = s.FirstAired
			}
			// TVDB Score is a popularity ranking, not a 0-10 user rating. The
			// search result's Score is the same scale; the extended record's is
			// preferred when present. Routed to video_popularity (not video_rating)
			// so accept= rules like `video_rating >= 7` keep their TMDb/Trakt
			// semantics when an entry passes through metainfo_tvdb.
			si.Popularity = ext.Score
			if si.Popularity == 0 {
				si.Popularity = s.Score
			}
			si.Status = ext.Status.Name
			si.Trailers = ext.TrailerURLs()
			si.ContentRating = ext.ContentRatingName()
			si.LastAirDate = itvdb.ParseDate(ext.LastAired)
			si.NextAirDate = itvdb.ParseDate(ext.NextAired)
			si.Aliases = ext.AliasNames()
			si.Cast = ext.ActorNames()
			if name := ext.OriginalName(lang); name != "" && name != ext.Name && name != s.Name {
				si.OriginalTitle = name
			}
		} else {
			// Extended fetch failed — fall back to search data only.
			si.Title = s.Name
			si.Description = s.Overview
			si.Poster = s.ImageURL
			si.Genres = s.Genres
			si.Network = s.Network
			si.Language = itvdb.LanguageName(s.Language)
			si.Country = itvdb.CountryName(s.Country)
			si.Popularity = s.Score
			if t := itvdb.ParseDate(s.FirstAired); !t.IsZero() {
				si.FirstAirDate = t
				si.PublishedDate = s.FirstAired
			}
		}
	}

	// Derive video_year from the first-air date when present — TVDB doesn't
	// return a separate Year field, but the first-air year is the canonical
	// premiere year for a series.
	if !si.FirstAirDate.IsZero() {
		si.Year = si.FirstAirDate.Year()
	}

	// Write series-level standard fields before the episode fetch so a fetch
	// failure doesn't prevent them from being set. TVDB is series-only — any
	// successfully-enriched entry is a series episode by construction.
	e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
	e.SetSeriesInfo(si)

	// Fetch episode-level detail if we have a specific episode.
	if ep.Season > 0 && ep.Episode > 0 {
		eps, err := p.fetchEpisodes(ctx, tc, s.ID, si.Title)
		if err != nil {
			return nil
		}
		si.Season = ep.Season
		si.Episode = ep.Episode
		si.EpisodeID = series.EpisodeID(ep)
		for _, ep2 := range eps {
			if ep2.SeasonNumber == ep.Season && ep2.EpisodeNumber == ep.Episode {
				e.Set("tvdb_episode_id", ep2.ID) // TVDB internal numeric episode ID
				si.EpisodeTitle = ep2.Name
				si.EpisodeDescription = ep2.Overview
				si.EpisodeAirDate = itvdb.ParseDate(ep2.AirDate)
				si.Runtime = ep2.Runtime
				si.EpisodeImage = ep2.Image
				break
			}
		}
		e.SetSeriesInfo(si)
	}

	return nil
}

func (p *tvdbPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if err := p.annotate(ctx, tc, e); err != nil {
			tc.Logger.Warn("metainfo_tvdb error", "entry", e.Title, "err", err)
		}
	}
	return entries, nil
}

// searchSeries resolves TVDB search results for name, using the cache where
// possible. If the name ends with a trailing 4-digit year (e.g. "Dark 2017"),
// two queries are dispatched in parallel — one with the year, one without.
// The full-name result is preferred; the stripped result is used as a fallback.
// Both results are cached individually regardless of outcome.
//
// Cache semantics: a non-empty cached result for the full name short-circuits
// immediately. A cached *empty* result does not block the stripped search,
// because the empty result may have been stored before this fallback logic
// existed, or the year may have been the reason for the miss.
func (p *tvdbPlugin) searchSeries(ctx context.Context, tc *plugin.TaskContext, name string) []itvdb.Series {
	stripped, definitive := stripTrailingYear(name)
	hasYear := stripped != name

	// Fast path: full name cached with results.
	fullCached, fullInCache := p.cache.Get(name)
	if fullInCache && len(fullCached) > 0 {
		tc.Logger.Debug("metainfo_tvdb: search cache hit", "series", name)
		return fullCached
	}

	if !hasYear {
		// No trailing year — single search (or honour cached empty result).
		if fullInCache {
			return nil
		}
		return p.fetchSearch(ctx, tc, name)
	}

	if definitive {
		// Year is parenthesized — unambiguously a production year.
		// Search only the stripped name; no need to try the full name.
		tc.Logger.Debug("metainfo_tvdb: stripping parenthesized year from search",
			"original", name, "stripped", stripped)
		if results, ok := p.cache.Get(stripped); ok {
			tc.Logger.Debug("metainfo_tvdb: stripped search cache hit", "series", stripped)
			return results
		}
		return p.fetchSearch(ctx, tc, stripped)
	}

	// Bare trailing year — ambiguous; run both queries in parallel.
	// A cached empty result for the full name does not block the stripped search.
	strippedCached, strippedInCache := p.cache.Get(stripped)

	if fullInCache && strippedInCache {
		if len(fullCached) > 0 {
			return fullCached
		}
		if len(strippedCached) > 0 {
			tc.Logger.Debug("metainfo_tvdb: returning stripped search cached result",
				"original", name, "stripped", stripped)
			return strippedCached
		}
		return nil
	}

	tc.Logger.Debug("metainfo_tvdb: parallel search triggered by trailing year",
		"series", name, "stripped", stripped)

	type result struct{ results []itvdb.Series }
	fullCh := make(chan result, 1)
	strippedCh := make(chan result, 1)

	if fullInCache {
		fullCh <- result{fullCached}
	} else {
		go func() { fullCh <- result{p.fetchSearch(ctx, tc, name)} }()
	}
	if strippedInCache {
		strippedCh <- result{strippedCached}
	} else {
		go func() { strippedCh <- result{p.fetchSearch(ctx, tc, stripped)} }()
	}

	fullRes := <-fullCh
	strippedRes := <-strippedCh

	if len(fullRes.results) > 0 {
		return fullRes.results
	}
	if len(strippedRes.results) > 0 {
		tc.Logger.Debug("metainfo_tvdb: results found with year stripped",
			"original", name, "stripped", stripped)
		return strippedRes.results
	}
	return nil
}

// fetchSearch performs a single live TVDB search, caches the result, and logs timing.
func (p *tvdbPlugin) fetchSearch(ctx context.Context, tc *plugin.TaskContext, name string) []itvdb.Series {
	t0 := time.Now()
	results, err := p.client.SearchSeries(ctx, name)
	if err != nil {
		tc.Logger.Warn("metainfo_tvdb: search failed", "series", name, "err", err)
		return nil
	}
	if len(results) > 0 {
		p.cache.Set(name, results)
	}
	tc.Logger.Debug("metainfo_tvdb: search", "series", name, "duration", time.Since(t0).Round(time.Millisecond))
	return results
}

// stripTrailingYear removes a trailing 4-digit year from name.
// Returns (stripped, definitive) where definitive is true when the year was
// parenthesized — unambiguously a production year — and false when it was a
// bare year that could be part of the title. Returns (name, false) if no
// trailing year is detected.
func stripTrailingYear(name string) (string, bool) {
	if m := reTrailingYearParen.FindStringSubmatch(name); m != nil {
		if y, _ := strconv.Atoi(m[2]); y >= 1900 && y <= 2099 {
			return strings.TrimSpace(m[1]), true // definitive: "(2019)"
		}
	}
	if m := reTrailingYearBare.FindStringSubmatch(name); m != nil {
		if y, _ := strconv.Atoi(m[2]); y >= 1900 && y <= 2099 {
			return strings.TrimSpace(m[1]), false // ambiguous: " 2019"
		}
	}
	return name, false
}

func (p *tvdbPlugin) fetchEpisodes(ctx context.Context, tc *plugin.TaskContext, id, name string) ([]itvdb.Episode, error) {
	if hit, ok := p.episodeCache.Get(id); ok {
		tc.Logger.Debug("metainfo_tvdb: episodes cache hit", "id", id)
		return hit.Episodes, nil
	}
	t0 := time.Now()
	eps, err := p.client.GetEpisodes(ctx, id)
	if err != nil {
		tc.Logger.Warn("metainfo_tvdb: episodes fetch failed", "id", id, "err", err)
		return nil, err
	}
	p.episodeCache.Set(id, cachedEpisodes{Name: name, Episodes: eps})
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

// Date and code-name helpers (LanguageName, CountryName, ParseDate) live in
// internal/tvdb so both this plugin and the tvdb_favorites source can share them.
