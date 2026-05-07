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
	if s.Country != "" {
		e.Set("tvdb_country", countryName(s.Country))
	}
	if s.ImageURL != "" {
		e.Set("tvdb_poster", s.ImageURL)
	}
	if t := parseFirstAired(s.FirstAired); !t.IsZero() {
		e.Set("tvdb_first_air_date", t)
	}
	if s.Score > 0 {
		e.Set("tvdb_score", s.Score)
	}

	// Always fetch extended data — it provides richer and more reliable
	// metadata (country, status, trailers, content ratings, aliases, etc.)
	// that the search endpoint returns inconsistently or not at all.
	if s.ID != "" {
		if ext, err := p.fetchExtended(ctx, tc, s.ID); err == nil {
			// Fill in search gaps with extended data.
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

			// Extended-only fields.
			country := s.Country
			if country == "" {
				country = ext.OriginalCountry
			}
			if country != "" {
				e.Set("tvdb_country", countryName(country))
			}
			if ext.Status.Name != "" {
				e.Set("tvdb_status", ext.Status.Name)
			}
			if urls := ext.TrailerURLs(); len(urls) > 0 {
				e.Set("tvdb_trailers", urls)
			}
			if rating := ext.ContentRatingName(); rating != "" {
				e.Set("tvdb_content_rating", rating)
			}
			if t := parseFirstAired(ext.LastAired); !t.IsZero() {
				e.Set("tvdb_last_air_date", t)
			}
			if t := parseFirstAired(ext.NextAired); !t.IsZero() {
				e.Set("tvdb_next_air_date", t)
			}
			if ext.Score > 0 && s.Score == 0 {
				e.Set("tvdb_score", ext.Score)
			}
			if aliases := ext.AliasNames(); len(aliases) > 0 {
				e.Set("tvdb_aliases", aliases)
			}
			if cast := ext.ActorNames(); len(cast) > 0 {
				e.Set("tvdb_cast", cast)
			}
			// Original-language title — only meaningful when the original language
			// differs from the display name returned by the search endpoint.
			lang := s.Language
			if lang == "" {
				lang = ext.Language
			}
			if name := ext.OriginalName(lang); name != "" && name != s.Name {
				e.Set("tvdb_original_title", name)
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
				if ep2.Runtime > 0 {
					e.Set("tvdb_episode_runtime", ep2.Runtime)
				}
				if ep2.Image != "" {
					e.Set("tvdb_episode_image", ep2.Image)
				}
				break
			}
		}
	}

	return nil
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
	fullCh     := make(chan result, 1)
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

	fullRes     := <-fullCh
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
	p.cache.Set(name, results)
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

// countryName maps ISO 3166-1 alpha-3 lowercase codes to English display names.
// Covers the most common TV-producing countries; falls back to the original
// code for anything not in the table.
func countryName(code string) string {
	if name, ok := iso3166[code]; ok {
		return name
	}
	return code
}

var iso3166 = map[string]string{
	"arg": "Argentina",
	"aus": "Australia",
	"aut": "Austria",
	"bel": "Belgium",
	"bra": "Brazil",
	"can": "Canada",
	"chl": "Chile",
	"chn": "China",
	"col": "Colombia",
	"cze": "Czech Republic",
	"dnk": "Denmark",
	"fin": "Finland",
	"fra": "France",
	"deu": "Germany",
	"hkg": "Hong Kong",
	"hun": "Hungary",
	"ind": "India",
	"idn": "Indonesia",
	"irl": "Ireland",
	"isr": "Israel",
	"ita": "Italy",
	"jpn": "Japan",
	"kor": "South Korea",
	"mex": "Mexico",
	"nld": "Netherlands",
	"nzl": "New Zealand",
	"nor": "Norway",
	"pol": "Poland",
	"prt": "Portugal",
	"rus": "Russia",
	"zaf": "South Africa",
	"esp": "Spain",
	"swe": "Sweden",
	"che": "Switzerland",
	"twn": "Taiwan",
	"tha": "Thailand",
	"tur": "Turkey",
	"gbr": "United Kingdom",
	"usa": "United States",
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
