// Package trakt provides a list sub-plugin that fetches movies or shows from a
// Trakt.tv list and emits one entry per item.
//
// Entries carry the item title and a canonical Trakt URL. They are suitable as
// title sources for the list= parameter of discover, series, and movies.
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
//	user_list     - slug of one of your own lists; replaces list when set
//	limit         - max results for public lists (default: 100)
package trakt

import (
	"context"
	"fmt"
	"regexp"

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
		Description: "fetch movies or shows from a Trakt.tv list as pipeline entries; usable as a standalone DAG source or inside series.list/movies.list",
		Role:        plugin.RoleSource,
		// trakt_slug is used for the entry URL but not set as a field.
		// media_type is set from the configured itemType ("shows" or "movies")
		// so downstream classifiers can dispatch without a metainfo step.
		Produces: []string{entry.FieldTitle, entry.FieldMediaType, entry.FieldSource},
		// Trakt list responses use extended=full and carry rating, votes, genres,
		// and overview directly. Surface them via Set*Info so downstream filters
		// (quality, condition, accept=video_rating >= 7, etc.) can act on the
		// list data without a redundant metainfo_trakt round-trip.
		MayProduce: []string{
			entry.FieldEnriched,
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
			entry.FieldMovieTagline,
			entry.FieldSeriesNetwork,
			entry.FieldSeriesStatus,
			entry.FieldSeriesFirstAirDate,
			"trakt_id",
			"trakt_imdb_id",
			"trakt_tmdb_id",
		},
		Factory:      newPlugin,
		Validate:     validate,
		IsListPlugin: true,
		Schema: []plugin.FieldSchema{
			{Key: "client_id", Type: plugin.FieldTypeString, Required: true, Hint: "Trakt API client ID"},
			{Key: "type", Type: plugin.FieldTypeEnum, Required: true, Enum: []string{"movies", "shows"}, Hint: "Content type"},
			{Key: "list", Type: plugin.FieldTypeEnum, Enum: []string{"watchlist", "trending", "popular", "watched", "ratings", "collection", "history", "recommendations"}, Hint: "List to fetch (default: watchlist)"},
			{Key: "user_list", Type: plugin.FieldTypeString, Hint: "Slug of one of your own Trakt lists (the end of its URL); replaces list"},
			{Key: "client_secret", Type: plugin.FieldTypeString, Hint: "OAuth client secret (for private lists)"},
			{Key: "access_token", Type: plugin.FieldTypeString, Hint: "OAuth bearer token (for private lists)"},
			{Key: "limit", Type: plugin.FieldTypeInt, Hint: "Maximum results (default 100)"},
		},
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
	if slug, ok := cfg["user_list"].(string); ok {
		if !userListSlug.MatchString(slug) {
			errs = append(errs, fmt.Errorf("trakt_list: user_list %q is not a Trakt list slug (lowercase letters, digits and dashes)", slug))
		}
		if _, ok := cfg["client_secret"].(string); !ok {
			if _, ok := cfg["access_token"].(string); !ok {
				errs = append(errs, fmt.Errorf("trakt_list: user_list needs client_secret or access_token, because your own lists are private"))
			}
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "trakt_list", "client_id", "client_secret", "type", "list", "user_list", "limit", "access_token")...)
	return errs
}

// userListSlug is the shape of a Trakt list slug, as in
// https://trakt.tv/users/<user>/lists/<slug>.
var userListSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type traktSourcePlugin struct {
	clientID     string
	clientSecret string          // set when using stored token auth
	staticToken  string          // set when using access_token from config
	authBucket   authBucketIface // non-nil when using stored token auth
	itemType     string
	list         string
	userList     string // slug of one of the user's own lists; replaces list
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

	userList, _ := cfg["user_list"].(string)

	p := &traktSourcePlugin{
		clientID: clientID,
		itemType: itemType,
		list:     list,
		userList: userList,
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

func (p *traktSourcePlugin) Name() string { return "trakt_list" }

// CacheKey returns a key that includes type and list so that two trakt_list
// instances with different parameters are cached independently.
func (p *traktSourcePlugin) CacheKey() string { return "trakt_list:" + p.itemType + ":" + p.listName() }

// listName is the list this instance reads: a user list's slug, prefixed so
// it can never collide with a built-in list of the same name.
func (p *traktSourcePlugin) listName() string {
	if p.userList != "" {
		return "user:" + p.userList
	}
	return p.list
}

func (p *traktSourcePlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	client, err := p.buildClient(ctx)
	if err != nil {
		return nil, err
	}

	var items []itrakt.Item
	if p.userList != "" {
		items, err = client.GetUserList(ctx, p.itemType, p.userList)
	} else {
		items, err = client.GetList(ctx, p.itemType, p.list, p.limit)
	}
	if err != nil {
		if len(items) == 0 {
			return nil, fmt.Errorf("trakt_list: fetch %s %s: %w", p.itemType, p.listName(), err)
		}
		tc.Logger.Warn("trakt_list: partial results due to pagination error", "count", len(items), "err", err)
	}

	entries := make([]*entry.Entry, 0, len(items))
	for _, item := range items {
		url := fmt.Sprintf("https://trakt.tv/%s/%s", p.itemType, item.IDs.Slug)
		e := entry.New(item.Title, url)
		e.Set(entry.FieldSource, "trakt_list:"+p.listName())
		if item.IDs.Trakt != 0 {
			e.Set("trakt_id", item.IDs.Trakt)
		}
		if item.IDs.IMDB != "" {
			e.Set("trakt_imdb_id", item.IDs.IMDB)
		}
		if item.IDs.TMDB != 0 {
			e.Set("trakt_tmdb_id", item.IDs.TMDB)
		}

		// Surface the same VideoInfo fields metainfo_trakt would populate from
		// the equivalent search call — both use extended=full and carry the
		// rating/votes/genres/overview that pipelines often want to filter on.
		// Enriched=true so downstream metainfo nodes (if present) know the
		// entry already has provider data.
		vi := itrakt.ToVideoInfo(item)
		if p.itemType == "shows" {
			e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
			e.SetSeriesInfo(entry.SeriesInfo{
				VideoInfo:    vi,
				Network:      item.Network,
				Status:       item.Status,
				FirstAirDate: item.FirstAiredDate(),
			})
		} else {
			e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
			e.SetMovieInfo(entry.MovieInfo{VideoInfo: vi, Tagline: item.Tagline})
		}

		entries = append(entries, e)
	}
	return entries, nil
}

func (p *traktSourcePlugin) buildClient(ctx context.Context) (*itrakt.Client, error) {
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
