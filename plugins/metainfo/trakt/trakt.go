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
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
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
		PluginPhase: plugin.PhaseMetainfo,
		Factory:     newPlugin,
		Validate:    validate,
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

func (p *traktMetaPlugin) Name() string        { return "metainfo_trakt" }
func (p *traktMetaPlugin) Phase() plugin.Phase { return plugin.PhaseMetainfo }

func (p *traktMetaPlugin) Annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	title, ok := p.parseTitle(e.Title)
	if !ok {
		tc.Logger.Warn("metainfo_trakt: title did not parse as "+p.itemType[:len(p.itemType)-1], "entry", e.Title)
		return nil
	}

	cacheKey := p.itemType + ":" + title
	singular := p.itemType[:len(p.itemType)-1] // "shows"→"show", "movies"→"movie"

	results, cached := p.cache.Get(cacheKey)
	if !cached {
		var err error
		results, err = p.client.Search(ctx, singular, title)
		if err != nil {
			tc.Logger.Warn("metainfo_trakt: search failed", "title", title, "err", err)
			return nil
		}
		p.cache.Set(cacheKey, results)
	}
	if len(results) == 0 {
		tc.Logger.Warn("metainfo_trakt: no results", "title", title, "type", singular, "entry", e.Title)
		return nil
	}

	r := results[0]
	e.Set("trakt_id", r.IDs.Trakt)
	e.Set("trakt_slug", r.IDs.Slug)
	e.Set("trakt_imdb_id", r.IDs.IMDB)
	e.Set("trakt_tmdb_id", r.IDs.TMDB)
	if p.itemType == "shows" && r.IDs.TVDB != 0 {
		e.Set("trakt_tvdb_id", r.IDs.TVDB)
	}
	e.Set("trakt_title", r.Title)
	e.Set("trakt_year", r.Year)
	e.Set("trakt_overview", r.Overview)
	e.Set("trakt_rating", r.Rating)
	e.Set("trakt_votes", r.Votes)
	if len(r.Genres) > 0 {
		e.Set("trakt_genres", strings.Join(r.Genres, ", "))
	}

	vi := entry.VideoInfo{
		GenericInfo:   entry.GenericInfo{Title: r.Title, Description: r.Overview},
		Year:          r.Year,
		Rating:        r.Rating,
		ImdbID:        r.IDs.IMDB,
		Genres:        r.Genres,
	}
	if p.itemType == "shows" {
		e.SetSeriesInfo(entry.SeriesInfo{VideoInfo: vi})
	} else {
		e.SetMovieInfo(entry.MovieInfo{VideoInfo: vi})
	}

	return nil
}

func (p *traktMetaPlugin) parseTitle(title string) (string, bool) {
	if p.itemType == "shows" {
		ep, ok := iseries.Parse(title)
		if !ok {
			return "", false
		}
		return ep.SeriesName, true
	}
	mv, ok := imovies.Parse(title)
	if !ok {
		return "", false
	}
	return mv.Title, true
}
