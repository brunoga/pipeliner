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
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itmdb "github.com/brunoga/pipeliner/internal/tmdb"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_tmdb",
		Description: "enrich movie entries with TMDb metadata (title, overview, genres, runtime)",
		PluginPhase: plugin.PhaseMetainfo,
		Factory:     newPlugin,
		Validate:    validate,
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
	client *itmdb.Client
	cache  *cache.Cache[[]itmdb.Movie]
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
		client: itmdb.New(apiKey),
		cache:  cache.NewPersistent[[]itmdb.Movie](ttl, db.Bucket("cache_metainfo_tmdb")),
	}, nil
}

func (p *tmdbPlugin) Name() string        { return "metainfo_tmdb" }
func (p *tmdbPlugin) Phase() plugin.Phase { return plugin.PhaseMetainfo }

func (p *tmdbPlugin) Annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	m, ok := imovies.Parse(e.Title)
	if !ok {
		tc.Logger.Warn("metainfo_tmdb: title did not parse as movie", "entry", e.Title)
		return nil
	}

	key := fmt.Sprintf("%s:%d", m.Title, m.Year)
	results, cached := p.cache.Get(key)
	if !cached {
		var err error
		results, err = p.client.SearchMovie(ctx, m.Title, m.Year)
		if err != nil {
			tc.Logger.Warn("metainfo_tmdb: search failed", "title", m.Title, "err", err)
			return nil
		}
		p.cache.Set(key, results)
	}
	if len(results) == 0 {
		tc.Logger.Warn("metainfo_tmdb: no results", "title", m.Title, "year", m.Year, "entry", e.Title)
		return nil
	}

	// Use the first (most popular) result.
	r := results[0]
	e.Set("tmdb_id", r.ID)

	mi := entry.MovieInfo{}
	mi.Enriched = true
	mi.Title = r.Title
	mi.Description = r.Overview
	mi.PublishedDate = r.ReleaseDate
	mi.Rating = r.VoteAverage
	mi.Popularity = r.Popularity
	if r.OrigTitle != r.Title {
		mi.OriginalTitle = r.OrigTitle
	}
	if len(r.ReleaseDate) >= 4 {
		if y, err := strconv.Atoi(r.ReleaseDate[:4]); err == nil {
			mi.Year = y
		}
	}

	if r.PosterPath != "" {
		mi.Poster = itmdb.ImageBaseURL + r.PosterPath
	}
	mi.Votes = r.VoteCount

	// Fetch extended detail for genres, runtime, tagline, imdb_id, cast,
	// trailers, content rating, language, and country.
	detail, err := p.client.GetMovie(ctx, r.ID)
	if err != nil {
		tc.Logger.Warn("metainfo_tmdb: detail fetch failed", "id", r.ID, "err", err)
		e.SetMovieInfo(mi)
		return nil
	}

	genres := make([]string, len(detail.Genres))
	for i, g := range detail.Genres {
		genres[i] = g.Name
	}
	mi.Runtime = detail.Runtime
	mi.Tagline = detail.Tagline
	mi.ImdbID = detail.ImdbID
	mi.Genres = genres
	mi.Language = iso639_1Name(detail.OriginalLanguage)
	if len(detail.ProductionCountries) > 0 {
		mi.Country = detail.ProductionCountries[0].Name
	}

	// Cast: top 10 actors in billing order.
	for i, c := range detail.Credits.Cast {
		if i >= 10 {
			break
		}
		if c.Name != "" {
			mi.Cast = append(mi.Cast, c.Name)
		}
	}

	// Trailers: YouTube only.
	for _, v := range detail.Videos.Results {
		if v.Site == "YouTube" && v.Type == "Trailer" && v.Key != "" {
			mi.Trailers = append(mi.Trailers, "https://www.youtube.com/watch?v="+v.Key)
		}
	}

	// Alternative titles.
	for _, t := range detail.AlternativeTitles.Titles {
		if t.Title != "" {
			mi.Aliases = append(mi.Aliases, t.Title)
		}
	}

	// Content rating: first certification found for the US.
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

	e.SetMovieInfo(mi)
	return nil
}

// iso639_1Name maps ISO 639-1 two-letter language codes to English display names.
func iso639_1Name(code string) string {
	names := map[string]string{
		"af": "Afrikaans", "ar": "Arabic", "bg": "Bulgarian", "bn": "Bengali",
		"cs": "Czech", "da": "Danish", "de": "German", "el": "Greek",
		"en": "English", "es": "Spanish", "et": "Estonian", "fa": "Persian",
		"fi": "Finnish", "fr": "French", "gu": "Gujarati", "he": "Hebrew",
		"hi": "Hindi", "hr": "Croatian", "hu": "Hungarian", "id": "Indonesian",
		"it": "Italian", "ja": "Japanese", "ko": "Korean", "lt": "Lithuanian",
		"lv": "Latvian", "mk": "Macedonian", "ml": "Malayalam", "mr": "Marathi",
		"ms": "Malay", "nl": "Dutch", "no": "Norwegian", "pa": "Punjabi",
		"pl": "Polish", "pt": "Portuguese", "ro": "Romanian", "ru": "Russian",
		"sk": "Slovak", "sl": "Slovenian", "sq": "Albanian", "sr": "Serbian",
		"sv": "Swedish", "sw": "Swahili", "ta": "Tamil", "te": "Telugu",
		"th": "Thai", "tl": "Filipino", "tr": "Turkish", "uk": "Ukrainian",
		"ur": "Urdu", "vi": "Vietnamese", "zh": "Chinese",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return code
}
