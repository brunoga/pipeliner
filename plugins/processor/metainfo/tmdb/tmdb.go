// Package tmdb provides a metainfo plugin that enriches movie entries with TMDb data.
//
// Config keys:
//
//	api_key   - TMDb API key (required)
//	cache_ttl - how long to cache search results, e.g. "24h" (default: "24h")
package tmdb

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/locale"
	"github.com/brunoga/pipeliner/internal/match"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itmdb "github.com/brunoga/pipeliner/internal/tmdb"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_tmdb",
		Description: "enrich movie entries with TMDb metadata (title, overview, genres, runtime)",
		Role:        plugin.RoleProcessor,
		MayProduce: []string{
			entry.FieldEnriched,
			entry.FieldTitle,
			entry.FieldMediaType,
			entry.FieldMovieTitle,
			entry.FieldMovieTagline,
			entry.FieldVideoYear,
			entry.FieldVideoLanguage,
			entry.FieldVideoOriginalTitle,
			entry.FieldVideoCountry,
			entry.FieldVideoGenres,
			entry.FieldVideoRating,
			entry.FieldVideoPoster,
			entry.FieldVideoRuntime,
			entry.FieldVideoAliases,
			entry.FieldVideoImdbID,
			entry.FieldVideoPopularity,
			entry.FieldVideoVotes,
			entry.FieldVideoHomepage,
			"tmdb_id",
		},
		// trakt_tmdb_id is used when present; video_year provides the year hint when present but
		// not required; no Requires group so pipelines without trakt upstream work.
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "api_key", Type: plugin.FieldTypeString, Required: true, Hint: "TMDb API key"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "24h", Hint: "How long to cache results"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_metainfo_tmdb", Display: "TMDb Search Cache"},
			{Name: "cache_metainfo_tmdb_detail", Display: "TMDb Detail Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "api_key", "metainfo_tmdb"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "cache_ttl", "metainfo_tmdb"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "metainfo_tmdb", "api_key", "cache_ttl")...)
	return errs
}

type tmdbPlugin struct {
	client      *itmdb.Client
	cache       *cache.Cache[[]itmdb.Movie]      // search results by "title:year"
	detailCache *cache.Cache[*itmdb.MovieDetail] // full detail by "detail:<id>"
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("metainfo_tmdb: 'api_key' is required")
	}

	ttl := 24 * time.Hour
	if v, _ := cfg["cache_ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("metainfo_tmdb: invalid cache_ttl %q: %w", v, err)
		}
		ttl = d
	}

	return &tmdbPlugin{
		client:      itmdb.New(apiKey),
		cache:       cache.NewPersistent[[]itmdb.Movie](ttl, db.Bucket("cache_metainfo_tmdb")),
		detailCache: cache.NewPersistent[*itmdb.MovieDetail](ttl, db.Bucket("cache_metainfo_tmdb_detail")),
	}, nil
}

func (p *tmdbPlugin) Name() string { return "metainfo_tmdb" }

func (p *tmdbPlugin) annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	// Fast path: if a Trakt (or other) TMDB ID is already on the entry, fetch
	// by ID directly and skip the search step. This avoids picking the wrong
	// result when multiple movies share the same title (e.g. "Michael" 1996 vs
	// 2026).
	if rawID, ok := e.Fields["trakt_tmdb_id"]; ok {
		if tmdbID, ok := rawID.(int); ok && tmdbID > 0 {
			return p.annotateByID(ctx, tc, e, tmdbID)
		}
	}

	// Parse the release title to extract the canonical movie title and year.
	// If parsing fails (entry has no quality markers or year suffix — e.g. a
	// clean list title like "Michael") fall back to the raw title + video_year
	// so that list-sourced entries can still be enriched.
	var searchTitle string
	var searchYear int

	if m, ok := imovies.Parse(e.Title); ok {
		searchTitle = m.Title
		searchYear = m.Year
	} else if y := entry.ReleaseYear(e); y > 0 {
		// Clean list title with known year (e.g. from trakt_list) — search directly.
		searchTitle = imovies.NormalizeTitle(e.Title)
		if searchTitle == "" {
			searchTitle = e.Title
		}
		searchYear = y
	} else {
		tc.Logger.Warn("metainfo_tmdb: title did not parse as movie", "entry", e.Title)
		return nil
	}

	// When the parsed year is 0 (title has no year suffix), use video_year as a
	// hint to avoid popularity-ranked mismatches for same-name films.
	if searchYear == 0 {
		searchYear = entry.ReleaseYear(e)
	}

	key := fmt.Sprintf("%s:%d", searchTitle, searchYear)
	results, cached := p.cache.Get(key)
	if !cached {
		var err error
		results, err = p.client.SearchMovie(ctx, searchTitle, searchYear)
		if err != nil {
			tc.Logger.Warn("metainfo_tmdb: search failed", "title", searchTitle, "err", err)
			return nil
		}
		// Year in the release name may be wrong (off-by-one, regional difference,
		// etc.). Retry without the year filter before giving up.
		if len(results) == 0 && searchYear > 0 {
			results, err = p.client.SearchMovie(ctx, searchTitle, 0)
			if err != nil {
				tc.Logger.Warn("metainfo_tmdb: search failed", "title", searchTitle, "err", err)
				return nil
			}
			// The yearless search returns popularity-ranked results that may have
			// nothing to do with the searched title (e.g. "Mother's Day" for "Mother").
			// Filter to only candidates whose title fuzzy-matches the search title.
			if len(results) > 0 {
				normSearch := match.Normalize(searchTitle)
				filtered := results[:0]
				for _, r := range results {
					if match.Fuzzy(normSearch, match.Normalize(r.Title)) {
						filtered = append(filtered, r)
					}
				}
				results = filtered
			}
		}
		// Only cache hits — an empty result must not be stored so the next
		// run retries the API (handles stale caches from wrong-year misses,
		// transient API failures, and movies not yet indexed on TMDb).
		if len(results) > 0 {
			p.cache.Set(key, results)
		}
	}
	if len(results) == 0 {
		tc.Logger.Warn("metainfo_tmdb: no results", "title", searchTitle, "year", searchYear, "entry", e.Title)
		return nil
	}

	r := results[0]
	e.Set("tmdb_id", r.ID)

	detail, err := p.fetchDetail(ctx, r.ID)
	if err != nil {
		tc.Logger.Warn("metainfo_tmdb: detail fetch failed", "id", r.ID, "err", err)
		// Populate with the partial info we already have from the search result.
		var mi entry.MovieInfo
		mi.Enriched = true
		mi.Title = r.Title
		mi.Description = r.Overview
		mi.PublishedDate = r.ReleaseDate
		mi.Rating = r.VoteAverage
		mi.Popularity = r.Popularity
		mi.Votes = r.VoteCount
		if r.OrigTitle != r.Title {
			mi.OriginalTitle = r.OrigTitle
		}
		if len(r.ReleaseDate) >= 4 {
			if y, err2 := strconv.Atoi(r.ReleaseDate[:4]); err2 == nil {
				mi.Year = y
			}
		}
		if r.PosterPath != "" {
			mi.Poster = itmdb.ImageBaseURL + r.PosterPath
		}
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
		e.SetMovieInfo(mi)
		return nil
	}
	populateFromDetail(e, detail)
	return nil
}

// annotateByID fetches a movie directly by its TMDb ID, bypassing the search
// step. Used when the entry already carries a trakt_tmdb_id so we never risk
// picking the wrong film due to title ambiguity or popularity ranking.
func (p *tmdbPlugin) annotateByID(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry, id int) error {
	detail, err := p.fetchDetail(ctx, id)
	if err != nil {
		tc.Logger.Warn("metainfo_tmdb: fetch by id failed", "id", id, "err", err)
		return nil
	}
	e.Set("tmdb_id", detail.ID)
	populateFromDetail(e, detail)
	return nil
}

// fetchDetail returns the full TMDb movie detail, using the detail cache to
// avoid a network round-trip when the same ID was already fetched this cycle.
func (p *tmdbPlugin) fetchDetail(ctx context.Context, id int) (*itmdb.MovieDetail, error) {
	key := fmt.Sprintf("detail:%d", id)
	if detail, ok := p.detailCache.Get(key); ok {
		return detail, nil
	}
	detail, err := p.client.GetMovie(ctx, id)
	if err != nil {
		return nil, err
	}
	p.detailCache.Set(key, detail)
	return detail, nil
}

// populateFromDetail fills MovieInfo on e from a fully-fetched TMDb detail response.
func populateFromDetail(e *entry.Entry, detail *itmdb.MovieDetail) {
	var mi entry.MovieInfo
	mi.Enriched = true
	mi.Title = detail.Title
	mi.Description = detail.Overview
	mi.PublishedDate = detail.ReleaseDate
	mi.Rating = detail.VoteAverage
	mi.Popularity = detail.Popularity
	mi.Votes = detail.VoteCount
	if detail.OrigTitle != detail.Title {
		mi.OriginalTitle = detail.OrigTitle
	}
	if len(detail.ReleaseDate) >= 4 {
		if y, err := strconv.Atoi(detail.ReleaseDate[:4]); err == nil {
			mi.Year = y
		}
	}
	if detail.PosterPath != "" {
		mi.Poster = itmdb.ImageBaseURL + detail.PosterPath
	}

	genres := make([]string, len(detail.Genres))
	for i, g := range detail.Genres {
		genres[i] = g.Name
	}
	mi.Runtime = detail.Runtime
	mi.Tagline = detail.Tagline
	mi.Homepage = detail.Homepage
	mi.ImdbID = detail.ImdbID
	mi.Genres = genres
	mi.Language = locale.LanguageName639_1(detail.OriginalLanguage)
	if len(detail.ProductionCountries) > 0 {
		mi.Country = detail.ProductionCountries[0].Name
	}
	for i, c := range detail.Credits.Cast {
		if i >= 10 {
			break
		}
		if c.Name != "" {
			mi.Cast = append(mi.Cast, c.Name)
		}
	}
	for _, v := range detail.Videos.Results {
		if v.Site == "YouTube" && v.Type == "Trailer" && v.Key != "" {
			mi.Trailers = append(mi.Trailers, "https://www.youtube.com/watch?v="+v.Key)
		}
	}
	for _, t := range detail.AlternativeTitles.Titles {
		if t.Title != "" {
			mi.Aliases = append(mi.Aliases, t.Title)
		}
	}
	for _, cr := range detail.ReleaseDates.Results {
		if cr.ISO == "US" {
			for _, rd := range cr.Dates {
				if rd.Certification != "" {
					mi.ContentRating = rd.Certification
					break
				}
			}
			break
		}
	}
	e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
	e.SetMovieInfo(mi)
}

func (p *tmdbPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if err := p.annotate(ctx, tc, e); err != nil {
			tc.Logger.Warn("metainfo_tmdb error", "entry", e.Title, "err", err)
		}
	}
	return entries, nil
}

// ISO 639-1 → display name mapping lives in internal/locale so both metainfo_tmdb
// and metainfo_trakt (which receive 2-letter codes from their respective APIs)
// share the same translation table.
