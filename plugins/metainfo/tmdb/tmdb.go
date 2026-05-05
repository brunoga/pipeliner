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
	"strings"
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
		return nil
	}

	// Use the first (most popular) result.
	r := results[0]
	e.Set("tmdb_id", r.ID)
	e.Set("tmdb_title", r.Title)
	e.Set("tmdb_original_title", r.OrigTitle)
	e.Set("tmdb_release_date", r.ReleaseDate)
	e.Set("tmdb_overview", r.Overview)
	e.Set("tmdb_popularity", r.Popularity)
	e.Set("tmdb_vote_average", r.VoteAverage)

	// Fetch extended detail for genres, runtime, imdb_id.
	detail, err := p.client.GetMovie(ctx, r.ID)
	if err != nil {
		tc.Logger.Warn("metainfo_tmdb: detail fetch failed", "id", r.ID, "err", err)
		return nil
	}
	e.Set("tmdb_runtime", detail.Runtime)
	e.Set("tmdb_tagline", detail.Tagline)
	e.Set("tmdb_imdb_id", detail.ImdbID)

	genres := make([]string, len(detail.Genres))
	for i, g := range detail.Genres {
		genres[i] = g.Name
	}
	e.Set("tmdb_genres", strings.Join(genres, ", "))

	return nil
}
