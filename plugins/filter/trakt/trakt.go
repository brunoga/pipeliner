// Package trakt implements a filter plugin that fetches a Trakt.tv list
// (watchlist, trending, popular, watched, ratings, or collection) and accepts
// entries whose parsed title matches something on that list.
//
// The fetched list is cached and refreshed according to the ttl setting
// (default 1h); one API call per TTL window regardless of how often the
// process runs.
//
// Config keys:
//
//	client_id     - Trakt API Client ID (required)
//	client_secret - OAuth client secret; when set, tokens are managed automatically
//	                via pipeliner.db (run `pipeliner auth trakt` to authorise).
//	access_token  - OAuth2 bearer token (alternative to client_secret; static)
//	type          - "shows" or "movies" (required)
//	list          - "trending", "popular", "watched", "watchlist", "ratings",
//	                "collection" (default: "watchlist")
//	limit         - max results for public lists (default: 100)
//	min_rating    - minimum user rating to include (ratings list only; 1–10)
//	ttl           - cache lifetime, e.g. "1h", "30m" (default: "1h")
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
		PluginName:  "trakt",
		PluginPhase: plugin.PhaseFilter,
		Description: "Accept entries matching titles from a Trakt.tv list (watchlist, trending, popular, etc.)",
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "client_id", "trakt"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "type", "trakt"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "type", "trakt", "shows", "movies"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "ttl", "trakt"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "trakt", "client_id", "client_secret", "type", "list", "limit", "min_rating", "ttl", "access_token")...)
	return errs
}

type traktFilter struct {
	clientID     string
	clientSecret string
	staticToken  string
	authBucket   store.Bucket
	itemType     string // "shows" or "movies"
	list         string
	limit        int
	minRating    int
	cache        *cache.Cache[[]string]
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	clientID, _ := cfg["client_id"].(string)
	if clientID == "" {
		return nil, fmt.Errorf("trakt filter: client_id is required")
	}

	itemType, _ := cfg["type"].(string)
	switch itemType {
	case "shows", "movies":
	case "":
		return nil, fmt.Errorf("trakt filter: type is required (shows or movies)")
	default:
		return nil, fmt.Errorf("trakt filter: type must be shows or movies, got %q", itemType)
	}

	list := "watchlist"
	if v, _ := cfg["list"].(string); v != "" {
		list = v
	}

	limit := 100
	if v, ok := cfg["limit"]; ok {
		switch n := v.(type) {
		case int:
			limit = n
		case int64:
			limit = int(n)
		case float64:
			limit = int(n)
		}
	}

	minRating := 0
	if v, ok := cfg["min_rating"]; ok {
		switch n := v.(type) {
		case int:
			minRating = n
		case int64:
			minRating = int(n)
		case float64:
			minRating = int(n)
		}
	}

	ttl := time.Hour
	if v, _ := cfg["ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("trakt filter: invalid ttl %q: %w", v, err)
		}
		ttl = d
	}

	p := &traktFilter{
		clientID:  clientID,
		itemType:  itemType,
		list:      list,
		limit:     limit,
		minRating: minRating,
		cache:     cache.NewPersistent[[]string](ttl, db.Bucket("cache_filter_trakt")),
	}
	if secret, _ := cfg["client_secret"].(string); secret != "" {
		p.clientSecret = secret
		p.authBucket = db.Bucket(itrakt.AuthBucket)
	} else if token, _ := cfg["access_token"].(string); token != "" {
		p.staticToken = token
	}
	return p, nil
}

func (p *traktFilter) Name() string        { return "trakt" }
func (p *traktFilter) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *traktFilter) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	titles, err := p.ensureTitles(ctx)
	if err != nil {
		tc.Logger.Warn("trakt: could not fetch list, skipping filter", "err", err)
		return nil
	}

	parsed, ok := p.parseTitle(e.Title)
	if !ok {
		return nil
	}

	norm := match.Normalize(parsed)
	for _, t := range titles {
		if match.Fuzzy(norm, t) {
			e.Accept()
			return nil
		}
	}
	return nil
}

func (p *traktFilter) buildClient(ctx context.Context) (*itrakt.Client, error) {
	if p.authBucket != nil {
		token, err := itrakt.GetValidAccessToken(ctx, p.authBucket, p.clientID, p.clientSecret)
		if err != nil {
			return nil, fmt.Errorf("trakt: %w", err)
		}
		return itrakt.NewWithToken(p.clientID, token), nil
	}
	if p.staticToken != "" {
		return itrakt.NewWithToken(p.clientID, p.staticToken), nil
	}
	return itrakt.New(p.clientID), nil
}

// ensureTitles returns the cached title list, fetching from Trakt if stale.
func (p *traktFilter) ensureTitles(ctx context.Context) ([]string, error) {
	// Cache key includes min_rating so different rating floors don't collide.
	key := fmt.Sprintf("%s:%s:%d", p.itemType, p.list, p.minRating)

	if titles, ok := p.cache.Get(key); ok {
		return titles, nil
	}

	client, err := p.buildClient(ctx)
	if err != nil {
		return nil, err
	}
	items, err := client.GetList(ctx, p.itemType, p.list, p.limit)
	if err != nil {
		return nil, err
	}

	var titles []string
	for _, it := range items {
		if p.minRating > 0 && it.UserRating < p.minRating {
			continue
		}
		titles = append(titles, match.Normalize(it.Title))
	}
	p.cache.Set(key, titles)
	return titles, nil
}

// parseTitle extracts the show name or movie title from an entry title.
func (p *traktFilter) parseTitle(title string) (string, bool) {
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
