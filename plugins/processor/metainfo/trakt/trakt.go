// Package trakt provides a metainfo plugin that annotates entries with Trakt.tv metadata.
//
// Config keys:
//
//	client_id  - Trakt API Client ID (required)
//	type       - "shows" or "movies" (required)
//	cache_ttl  - how long to cache search results, e.g. "24h" (default: "24h")
package trakt

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	iseries "github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_trakt",
		Description: "annotate entries with Trakt.tv metadata (rating, votes, genres, overview, external IDs)",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalNone,
		MayProduce: []string{
			entry.FieldEnriched,
			entry.FieldTitle,
			entry.FieldMediaType,
			entry.FieldDescription,
			entry.FieldVideoYear,
			entry.FieldVideoGenres,
			entry.FieldVideoRating,
			entry.FieldVideoVotes,
			entry.FieldVideoLanguage,
			entry.FieldVideoCountry,
			entry.FieldVideoRuntime,
			entry.FieldVideoTrailers,
			entry.FieldVideoContentRating,
			entry.FieldVideoHomepage,
			entry.FieldVideoImdbID,
			entry.FieldVideoPoster,
			entry.FieldMovieTagline,
			entry.FieldSeriesNetwork,
			entry.FieldSeriesStatus,
			entry.FieldSeriesFirstAirDate,
			"trakt_id",
			"trakt_slug",
			"trakt_tmdb_id",
			"trakt_tvdb_id",
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "client_id", Type: plugin.FieldTypeString, Required: true, Hint: "Trakt API client ID"},
			{Key: "type", Type: plugin.FieldTypeEnum, Required: true, Enum: []string{"shows", "movies"}, Hint: "Content type"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Hint: "Search result cache lifetime (default 24h)"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_metainfo_trakt", Display: "Trakt Metainfo Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "client_id", "metainfo_trakt"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "type", "metainfo_trakt"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "type", "metainfo_trakt", "shows", "movies"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "cache_ttl", "metainfo_trakt"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "metainfo_trakt", "client_id", "type", "cache_ttl")...)
	return errs
}

type traktMetaPlugin struct {
	client   *itrakt.Client
	itemType string // "shows" or "movies"
	cache    *cache.Cache[[]itrakt.Item]
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	clientID, _ := cfg["client_id"].(string)
	if clientID == "" {
		return nil, fmt.Errorf("metainfo_trakt: client_id is required")
	}

	itemType, _ := cfg["type"].(string)
	switch itemType {
	case "shows", "movies":
	case "":
		return nil, fmt.Errorf("metainfo_trakt: type is required (shows or movies)")
	default:
		return nil, fmt.Errorf("metainfo_trakt: type must be shows or movies, got %q", itemType)
	}

	ttl := 24 * time.Hour
	if v, _ := cfg["cache_ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("metainfo_trakt: invalid cache_ttl %q: %w", v, err)
		}
		ttl = d
	}

	return &traktMetaPlugin{
		client:   itrakt.New(clientID),
		itemType: itemType,
		cache:    cache.NewPersistent[[]itrakt.Item](ttl, db.Bucket("cache_metainfo_trakt")),
	}, nil
}

func (p *traktMetaPlugin) Name() string { return "metainfo_trakt" }

func (p *traktMetaPlugin) annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	title, year, ok := p.parseTitle(e.Title)
	if !ok {
		tc.Logger.Warn("metainfo_trakt: title did not parse as "+p.itemType[:len(p.itemType)-1], "entry", e.Title)
		return nil
	}
	if year == 0 {
		// A clean list title carries no year of its own; an upstream list
		// source may have set one as a field.
		year = entry.ReleaseYear(e)
	}

	singular := p.itemType[:len(p.itemType)-1] // "shows"→"show", "movies"→"movie"

	results, ok := p.resolve(ctx, tc, e, singular, title)
	if !ok {
		tc.Logger.Warn("metainfo_trakt: no results", "title", title, "type", singular, "entry", e.Title)
		return nil
	}

	// Prefer the result whose title matches the searched one exactly (after
	// normalization) over Trakt's relevance ranking — a same-name spin-off or
	// companion entry can outrank the actual item. Falls back to results[0].
	r := pickItem(results, title, year)
	e.Set("trakt_id", r.IDs.Trakt)
	e.Set("trakt_slug", r.IDs.Slug)
	e.Set("trakt_tmdb_id", r.IDs.TMDB)
	if p.itemType == "shows" && r.IDs.TVDB != 0 {
		e.Set("trakt_tvdb_id", r.IDs.TVDB)
	}

	vi := itrakt.ToVideoInfo(r)
	if p.itemType == "shows" {
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		e.SetSeriesInfo(entry.SeriesInfo{
			VideoInfo:    vi,
			Network:      r.Network,
			Status:       r.Status,
			FirstAirDate: r.FirstAiredDate(),
		})
	} else {
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
		e.SetMovieInfo(entry.MovieInfo{VideoInfo: vi, Tagline: r.Tagline})
	}

	return nil
}

func (p *traktMetaPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if err := p.annotate(ctx, tc, e); err != nil {
			tc.Logger.Warn("metainfo_trakt error", "entry", e.Title, "err", err)
		}
	}
	return entries, nil
}

// parseTitle extracts the item's name and the year the release names, 0 when
// it names none. The year is what separates two items sharing a title, and a
// release name is where it is written — the video_year field is set by the
// metainfo plugins, so on a raw release it is not there yet.
func (p *traktMetaPlugin) parseTitle(title string) (string, int, bool) {
	if p.itemType == "shows" {
		ep, ok := iseries.Parse(title)
		if !ok {
			return "", 0, false
		}
		return ep.SeriesName, ep.SeriesYear, true
	}
	mv, ok := imovies.Parse(title)
	if !ok {
		return "", 0, false
	}
	return mv.Title, mv.Year, true
}

// pickItem returns the search result that best matches the searched title and
// year, preferring it over Trakt's relevance ranking — a same-name spin-off,
// remake or companion entry can outrank the actual item.
//
// The year is what separates two items that genuinely share a title, which
// relevance cannot: Trakt's search is given a string, so for "Michael" it
// answers with whichever Michael is more popular. It is a preference and not a
// filter, because the year on a release is frequently absent and occasionally
// wrong; year 0 on either side matches anything, and known years are compared
// with match.YearsCompatible's ±1 tolerance for regional windows.
//
// results must be non-empty.
func pickItem(results []itrakt.Item, title string, year int) itrakt.Item {
	norm := match.Normalize(title)
	var titleOnly *itrakt.Item
	for i, r := range results {
		if match.Normalize(r.Title) != norm {
			continue
		}
		if match.YearsCompatible(year, r.Year) {
			return r
		}
		if titleOnly == nil {
			titleOnly = &results[i]
		}
	}
	if titleOnly != nil {
		return *titleOnly
	}
	// No title matched. A compatible year is then the only signal left that
	// is better than relevance alone.
	if year != 0 {
		for _, r := range results {
			if r.Year != 0 && match.YearsCompatible(year, r.Year) {
				return r
			}
		}
	}
	return results[0]
}

// resolve returns the candidate items for an entry: by an id the entry already
// carries where there is one, otherwise by searching for its parsed title.
//
// Every id namespace is tried in turn, because which one an entry has depends
// on what ran upstream — trakt_list leaves its own trakt_id, metainfo_tvdb
// leaves a tvdb_id, an indexer may have published an IMDb id. A name search
// knows only the string, and a string is not an identity: it cannot separate
// two shows called "Tomb Raider" or two films called "Michael", and Trakt
// answers it with whichever is more popular.
//
// Cached either way, under a key that records which question was asked.
func (p *traktMetaPlugin) resolve(ctx context.Context, tc *plugin.TaskContext,
	e *entry.Entry, singular, title string) ([]itrakt.Item, bool) {
	for _, src := range []struct {
		idType string
		read   func(*entry.Entry) string
	}{
		{"trakt", entry.TraktID},
		{"imdb", entry.IMDBID},
		{"tmdb", entry.TMDBID},
		{"tvdb", entry.TVDBID}, // shows only in practice; Trakt ignores it for films
	} {
		id := src.read(e)
		if id == "" {
			continue
		}
		if items, ok := p.lookup(ctx, tc, singular, src.idType, id); ok {
			return items, true
		}
	}

	key := p.itemType + ":" + title
	if results, cached := p.cache.Get(key); cached {
		return results, len(results) > 0
	}
	results, err := p.client.Search(ctx, singular, title)
	if err != nil {
		tc.Logger.Warn("metainfo_trakt: search failed", "title", title, "err", err)
		return nil, false
	}
	if len(results) > 0 {
		p.cache.Set(key, results)
	}
	return results, len(results) > 0
}

// lookup resolves one id namespace, reporting whether Trakt answered with
// anything. A miss is not fatal: the caller tries the next namespace and then
// the name search, so an id Trakt does not know costs the entry nothing.
func (p *traktMetaPlugin) lookup(ctx context.Context, tc *plugin.TaskContext,
	singular, idType, id string) ([]itrakt.Item, bool) {
	key := p.itemType + ":" + idType + ":" + id
	if results, cached := p.cache.Get(key); cached {
		return results, len(results) > 0
	}
	results, err := p.client.LookupByID(ctx, singular, idType, id)
	if err != nil {
		tc.Logger.Warn("metainfo_trakt: lookup by id failed",
			"id_type", idType, "id", id, "err", err)
		return nil, false
	}
	if len(results) == 0 {
		tc.Logger.Debug("metainfo_trakt: no item for id", "id_type", idType, "id", id)
		return nil, false
	}
	p.cache.Set(key, results)
	tc.Logger.Debug("metainfo_trakt: resolved by id", "id_type", idType, "id", id, "title", results[0].Title)
	return results, true
}
