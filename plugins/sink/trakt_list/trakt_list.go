// Package trakt_list provides the trakt_list_update sink, which adds or
// removes accepted entries on a Trakt.tv user list or watchlist.
//
// Unlike TheTVDB favorites, Trakt lists support removal, so this sink covers
// full remote list hygiene: prune ended shows from the watchlist, mirror a
// filtered list, auto-add discovered premieres.
//
// All entries of a run are collected into a single batched request — Trakt's
// sync endpoints take arrays of items.
//
// Config keys:
//
//	client_id     - Trakt API Client ID (required)
//	client_secret - OAuth client secret; tokens managed automatically via
//	                pipeliner.db (run `pipeliner auth trakt` to authorise)
//	access_token  - OAuth2 bearer token (alternative to client_secret; static)
//	list          - "watchlist" or a personal list slug (required)
//	action        - "add" or "remove" (default: "add")
//	type          - "shows" or "movies"; when omitted, inferred per entry from
//	                the media_type field
package trakt_list

import (
	"context"
	"fmt"
	"strconv"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

const pluginName = "trakt_list_update"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "add or remove accepted entries on a Trakt.tv user list or watchlist",
		Role:        plugin.RoleSink,
		// Trakt matches items on any of its known ID namespaces; one usable ID
		// field is enough.
		Requires: plugin.RequireAny(
			"trakt_id",
			"trakt_imdb_id", entry.FieldVideoImdbID,
			"trakt_tmdb_id", "tmdb_id",
			"trakt_tvdb_id", "tvdb_id",
		),
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "client_id", Type: plugin.FieldTypeString, Required: true, Hint: "Trakt API client ID"},
			{Key: "list", Type: plugin.FieldTypeString, Required: true, Hint: "\"watchlist\" or a personal list slug"},
			{Key: "action", Type: plugin.FieldTypeEnum, Enum: []string{"add", "remove"}, Default: "add", Hint: "Add or remove the entries"},
			{Key: "type", Type: plugin.FieldTypeEnum, Enum: []string{"shows", "movies"}, Hint: "Item type (default: infer from media_type)"},
			{Key: "client_secret", Type: plugin.FieldTypeString, Hint: "OAuth client secret (stored token auth)"},
			{Key: "access_token", Type: plugin.FieldTypeString, Hint: "OAuth bearer token (static auth)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "client_id", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "list", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "action", pluginName, "add", "remove"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "type", pluginName, "shows", "movies"); err != nil {
		errs = append(errs, err)
	}
	secret, _ := cfg["client_secret"].(string)
	token, _ := cfg["access_token"].(string)
	if secret == "" && token == "" {
		errs = append(errs, fmt.Errorf("%s: list mutation is always authenticated — set client_secret (stored oauth token; run `pipeliner auth trakt`) or access_token", pluginName))
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, "client_id", "client_secret", "access_token", "list", "action", "type")...)
	return errs
}

type traktListSink struct {
	clientID     string
	clientSecret string       // set when using stored token auth
	staticToken  string       // set when using access_token from config
	authBucket   store.Bucket // non-nil when using stored token auth
	list         string
	action       string // "add" or "remove"
	itemType     string // "shows", "movies", or "" (infer from media_type)
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	clientID, _ := cfg["client_id"].(string)
	if clientID == "" {
		return nil, fmt.Errorf("%s: client_id is required", pluginName)
	}
	list, _ := cfg["list"].(string)
	if list == "" {
		return nil, fmt.Errorf("%s: list is required", pluginName)
	}

	action, _ := cfg["action"].(string)
	switch action {
	case "":
		action = "add"
	case "add", "remove":
	default:
		return nil, fmt.Errorf("%s: action must be \"add\" or \"remove\", got %q", pluginName, action)
	}

	itemType, _ := cfg["type"].(string)
	switch itemType {
	case "", "shows", "movies":
	default:
		return nil, fmt.Errorf("%s: type must be \"shows\" or \"movies\", got %q", pluginName, itemType)
	}

	p := &traktListSink{
		clientID: clientID,
		list:     list,
		action:   action,
		itemType: itemType,
	}

	if secret, _ := cfg["client_secret"].(string); secret != "" {
		p.clientSecret = secret
		p.authBucket = db.Bucket(itrakt.AuthBucket)
	} else if token, _ := cfg["access_token"].(string); token != "" {
		p.staticToken = token
	} else {
		return nil, fmt.Errorf("%s: set client_secret (stored oauth token; run `pipeliner auth trakt`) or access_token", pluginName)
	}

	return p, nil
}

func (p *traktListSink) Name() string { return pluginName }

func (p *traktListSink) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	verb, prep := "add", "to"
	if p.action == "remove" {
		verb, prep = "remove", "from"
	}

	// Collect the batch: one add/remove request per run — Trakt's sync
	// endpoints take arrays of items.
	var body itrakt.ListItemsBody
	var batched []*entry.Entry
	var batchedIDs []itrakt.ItemIDs
	for _, e := range entries {
		ids := itemIDs(e)
		if ids.IsZero() {
			e.Fail(pluginName + ": entry has no usable trakt/imdb/tmdb/tvdb id")
			continue
		}

		itemType := p.itemType
		if itemType == "" {
			switch e.GetString(entry.FieldMediaType) {
			case entry.MediaTypeSeries:
				itemType = "shows"
			case entry.MediaTypeMovie:
				itemType = "movies"
			default:
				e.Fail(pluginName + ": cannot infer item type — set type= or ensure media_type is present")
				continue
			}
		}

		if tc.DryRun {
			e.Accept(fmt.Sprintf("%s: would %s %q %s trakt list %q", pluginName, verb, e.Title, prep, p.list))
			tc.Logger.Info(pluginName+": dry-run", "would", verb, "title", e.Title, "list", p.list, "type", itemType)
			continue
		}

		if itemType == "shows" {
			body.Shows = append(body.Shows, itrakt.ListItem{IDs: ids})
		} else {
			body.Movies = append(body.Movies, itrakt.ListItem{IDs: ids})
		}
		batched = append(batched, e)
		batchedIDs = append(batchedIDs, ids)
	}

	if tc.DryRun || body.Empty() {
		return nil
	}

	client, err := p.buildClient(ctx)
	if err != nil {
		failAll(batched, err)
		tc.Logger.Warn(pluginName+": authentication failed", "err", err)
		return nil
	}

	var resp *itrakt.SyncResponse
	if p.action == "remove" {
		resp, err = client.RemoveListItems(ctx, p.list, body)
	} else {
		resp, err = client.AddListItems(ctx, p.list, body)
	}
	if err != nil {
		failAll(batched, err)
		tc.Logger.Warn(pluginName+": list update failed", "list", p.list, "action", p.action, "err", err)
		return nil
	}

	// Fail the entries Trakt reported as unmatched so chained sinks and the
	// commit phase do not treat them as done.
	notFound := append(resp.NotFound.Shows, resp.NotFound.Movies...) //nolint:gocritic // intentionally merged copy
	for i, e := range batched {
		if idsListed(notFound, batchedIDs[i]) {
			e.Fail(pluginName + ": trakt could not match any of the entry's ids")
		}
	}

	tc.Logger.Info(pluginName+": list updated",
		"list", p.list, "action", p.action,
		"sent", len(batched),
		"added", countOf(resp.Added), "deleted", countOf(resp.Deleted),
		"existing", countOf(resp.Existing), "not_found", len(notFound))
	return nil
}

func (p *traktListSink) buildClient(ctx context.Context) (*itrakt.Client, error) {
	if p.authBucket != nil {
		token, err := itrakt.GetValidAccessToken(ctx, p.authBucket, p.clientID, p.clientSecret)
		if err != nil {
			return nil, err
		}
		return itrakt.NewWithToken(p.clientID, token), nil
	}
	return itrakt.NewWithToken(p.clientID, p.staticToken), nil
}

func failAll(entries []*entry.Entry, err error) {
	for _, e := range entries {
		e.Fail(pluginName + ": " + err.Error())
	}
}

func countOf(m map[string]int) int {
	total := 0
	for _, n := range m {
		total += n
	}
	return total
}

// idsListed reports whether any ID of want appears in the not_found items.
func idsListed(items []itrakt.ListItem, want itrakt.ItemIDs) bool {
	for _, it := range items {
		ids := it.IDs
		if want.Trakt != 0 && ids.Trakt == want.Trakt {
			return true
		}
		if want.IMDB != "" && ids.IMDB == want.IMDB {
			return true
		}
		if want.TMDB != 0 && ids.TMDB == want.TMDB {
			return true
		}
		if want.TVDB != 0 && ids.TVDB == want.TVDB {
			return true
		}
	}
	return false
}

// itemIDs builds the Trakt ids object from whichever ID fields are present on
// the entry. Field producers: trakt_list source and metainfo_trakt set
// trakt_id/trakt_imdb_id/trakt_tmdb_id/trakt_tvdb_id; metainfo_tmdb sets
// tmdb_id; metainfo_tvdb and tvdb_favorites set tvdb_id (string); several
// metainfo plugins set video_imdb_id.
func itemIDs(e *entry.Entry) itrakt.ItemIDs {
	ids := itrakt.ItemIDs{
		Trakt: intField(e, "trakt_id"),
		TMDB:  intField(e, "trakt_tmdb_id", "tmdb_id"),
		TVDB:  intField(e, "trakt_tvdb_id", "tvdb_id"),
	}
	for _, key := range []string{"trakt_imdb_id", entry.FieldVideoImdbID} {
		if v := e.GetString(key); v != "" {
			ids.IMDB = v
			break
		}
	}
	return ids
}

// intField returns the first positive integer value among keys, tolerating
// int, int64, float64, and numeric-string field values.
func intField(e *entry.Entry, keys ...string) int {
	for _, key := range keys {
		v, ok := e.Get(key)
		if !ok {
			continue
		}
		switch n := v.(type) {
		case int:
			if n > 0 {
				return n
			}
		case int64:
			if n > 0 {
				return int(n)
			}
		case float64:
			if int(n) > 0 {
				return int(n)
			}
		case string:
			if id, err := strconv.Atoi(n); err == nil && id > 0 {
				return id
			}
		}
	}
	return 0
}
