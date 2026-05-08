// Package trakt provides a from plugin that fetches movies or shows from a
// Trakt.tv list and emits one entry per item.
//
// Entries carry the item title and a canonical Trakt URL. They are suitable as
// title sources for discover.from, series.from, and movies.from.
//
// Config keys:
//
//	client_id     - Trakt API Client ID (required)
//	client_secret - OAuth client secret; when set, tokens are managed automatically
//	                via pipeliner.db (run `pipeliner auth trakt` to authorise).
//	access_token  - OAuth2 bearer token (alternative to client_secret; static)
//	type          - "movies" or "shows" (required)
//	list          - list name: "watchlist", "trending", "popular", "watched",
//	                "ratings", "collection" (default: "watchlist")
//	limit         - max results for public lists (default: 100)
package trakt

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

// authBucketIface is satisfied by store.Bucket and used to pass to itrakt auth functions.
type authBucketIface = store.Bucket

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
	errs = append(errs, plugin.OptUnknownKeys(cfg, "trakt_list", "client_id", "client_secret", "type", "list", "limit", "access_token")...)
	return errs
}

type traktInputPlugin struct {
	clientID     string
	clientSecret string          // set when using stored token auth
	staticToken  string          // set when using access_token from config
	authBucket   authBucketIface // non-nil when using stored token auth
	itemType     string
	list         string
	limit        int
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
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

	p := &traktInputPlugin{
		clientID: clientID,
		itemType: itemType,
		list:     list,
		limit:    limit,
	}

	if secret, _ := cfg["client_secret"].(string); secret != "" {
		p.clientSecret = secret
		p.authBucket = db.Bucket(itrakt.AuthBucket)
	} else if token, _ := cfg["access_token"].(string); token != "" {
		p.staticToken = token
	}

	return p, nil
}

func (p *traktInputPlugin) Name() string        { return "trakt_list" }
func (p *traktInputPlugin) Phase() plugin.Phase { return plugin.PhaseFrom }

// CacheKey returns a key that includes type and list so that two trakt_list
// instances with different parameters are cached independently.
func (p *traktInputPlugin) CacheKey() string { return "trakt_list:" + p.itemType + ":" + p.list }

func (p *traktInputPlugin) Run(ctx context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	client, err := p.buildClient(ctx)
	if err != nil {
		return nil, err
	}

	items, err := client.GetList(ctx, p.itemType, p.list, p.limit)
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

func (p *traktInputPlugin) buildClient(ctx context.Context) (*itrakt.Client, error) {
	if p.authBucket != nil {
		token, err := itrakt.GetValidAccessToken(ctx, p.authBucket, p.clientID, p.clientSecret)
		if err != nil {
			return nil, fmt.Errorf("trakt_list: %w", err)
		}
		return itrakt.NewWithToken(p.clientID, token), nil
	}
	if p.staticToken != "" {
		return itrakt.NewWithToken(p.clientID, p.staticToken), nil
	}
	return itrakt.New(p.clientID), nil
}
