// Package trakt provides a from plugin that fetches movies or shows from a
// Trakt.tv list and emits one entry per item.
//
// Entries carry the item title and a canonical Trakt URL. They are suitable as
// title sources for discover.from, series.from, and movies.from.
//
// Config keys:
//
//	client_id    - Trakt API Client ID (required)
//	access_token - OAuth2 bearer token (required for watchlist/ratings/collection)
//	type         - "movies" or "shows" (required)
//	list         - list name: "watchlist", "trending", "popular", "watched",
//	               "ratings", "collection" (default: "watchlist")
//	limit        - max results for public lists (default: 100)
package trakt

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "trakt_list",
		Description: "fetch movies or shows from a Trakt.tv list as pipeline entries",
		PluginPhase: plugin.PhaseFrom,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "client_id", "trakt_list"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "type", "trakt_list"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "type", "trakt_list", "movies", "shows"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "trakt_list", "client_id", "type", "list", "limit", "access_token")...)
	return errs
}

type traktInputPlugin struct {
	client   *itrakt.Client
	itemType string
	list     string
	limit    int
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	clientID, _ := cfg["client_id"].(string)
	if clientID == "" {
		return nil, fmt.Errorf("trakt_list: client_id is required")
	}

	itemType, _ := cfg["type"].(string)
	switch itemType {
	case "movies", "shows":
	case "":
		return nil, fmt.Errorf("trakt_list: type is required (movies or shows)")
	default:
		return nil, fmt.Errorf("trakt_list: type must be \"movies\" or \"shows\", got %q", itemType)
	}

	list, _ := cfg["list"].(string)
	if list == "" {
		list = "watchlist"
	}

	limit := 100
	if v, ok := cfg["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	var client *itrakt.Client
	if token, _ := cfg["access_token"].(string); token != "" {
		client = itrakt.NewWithToken(clientID, token)
	} else {
		client = itrakt.New(clientID)
	}

	return &traktInputPlugin{
		client:   client,
		itemType: itemType,
		list:     list,
		limit:    limit,
	}, nil
}

func (p *traktInputPlugin) Name() string        { return "trakt_list" }
func (p *traktInputPlugin) Phase() plugin.Phase { return plugin.PhaseFrom }

// CacheKey returns a key that includes type and list so that two trakt_list
// instances with different parameters (e.g. watchlist vs ratings) are cached
// independently.
func (p *traktInputPlugin) CacheKey() string { return "trakt_list:" + p.itemType + ":" + p.list }

func (p *traktInputPlugin) Run(ctx context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	items, err := p.client.GetList(ctx, p.itemType, p.list, p.limit)
	if err != nil {
		return nil, fmt.Errorf("trakt_list: fetch %s %s: %w", p.itemType, p.list, err)
	}

	entries := make([]*entry.Entry, 0, len(items))
	for _, item := range items {
		url := fmt.Sprintf("https://trakt.tv/%s/%s", p.itemType, item.IDs.Slug)
		e := entry.New(item.Title, url)
		if item.Year > 0 {
			e.Set("trakt_year", item.Year)
		}
		if item.IDs.Trakt != 0 {
			e.Set("trakt_id", item.IDs.Trakt)
		}
		if item.IDs.IMDB != "" {
			e.Set("trakt_imdb_id", item.IDs.IMDB)
		}
		if item.IDs.TMDB != 0 {
			e.Set("trakt_tmdb_id", item.IDs.TMDB)
		}
		entries = append(entries, e)
	}
	return entries, nil
}
