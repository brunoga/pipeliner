// Package tvdb_favorites provides the tvdb_favorites_add sink, which adds
// accepted entries to the authenticated user's TheTVDB favorites list, or —
// with legacy v3 credentials — removes them.
//
// TheTVDB's v4 API only supports adding favorites: per the official swagger,
// /user/favorites accepts GET and POST only, so there is no removal endpoint.
// action="remove" therefore requires the legacy v3 API, which is enabled by
// setting legacy_user_key and legacy_user_name (the "unique ID"/userkey and
// username shown in the thetvdb.com account dashboard). Without both keys,
// action="remove" is rejected at validation time. As an alternative, filter
// on series_status into a local list (list_add) or a Trakt list
// (trakt_list_update, which does support removal) and use that list as the
// series list source instead of raw favorites.
//
// Config keys:
//
//	api_key         - TheTVDB API key (required)
//	user_pin        - User PIN from thetvdb.com (required; enables favorites access)
//	action          - "add" (default) or "remove" (requires legacy v3 credentials)
//	legacy_user_key - legacy v3 account identifier / userkey (required for action="remove")
//	legacy_user_name - legacy v3 username (required for action="remove")
package tvdb_favorites

import (
	"context"
	"fmt"
	"strconv"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

const pluginName = "tvdb_favorites_add"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "add accepted entries to the user's TheTVDB favorites list (or remove them via legacy v3 credentials)",
		Role:        plugin.RoleSink,
		Requires:    plugin.RequireAll("tvdb_id"),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "api_key", Type: plugin.FieldTypeString, Required: true, Hint: "TheTVDB v4 API key"},
			{Key: "user_pin", Type: plugin.FieldTypeString, Required: true, Hint: "TheTVDB user PIN"},
			{Key: "action", Type: plugin.FieldTypeEnum, Enum: []string{"add", "remove"}, Default: "add", Hint: "\"remove\" needs legacy_user_key + legacy_user_name (v4 cannot remove favorites)"},
			{Key: "legacy_user_key", Type: plugin.FieldTypeString, Hint: "Legacy v3 userkey (account identifier) — required for action=\"remove\""},
			{Key: "legacy_user_name", Type: plugin.FieldTypeString, Hint: "Legacy v3 username — required for action=\"remove\""},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "api_key", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "user_pin", pluginName); err != nil {
		errs = append(errs, err)
	}
	action, _ := cfg["action"].(string)
	legacyKey, _ := cfg["legacy_user_key"].(string)
	legacyName, _ := cfg["legacy_user_name"].(string)
	switch action {
	case "", "add":
		// ok
	case "remove":
		if legacyKey == "" || legacyName == "" {
			errs = append(errs, fmt.Errorf("%s: action \"remove\" requires legacy_user_key and legacy_user_name — thetvdb's v4 api has no favorites-removal endpoint (/user/favorites accepts GET and POST only), so removal goes through the legacy v3 api which needs the userkey and username from your thetvdb.com account dashboard; alternatively, filter on series_status into a local list (list_add) or a trakt list (trakt_list_update supports removal) and use that list as the series list source instead of raw favorites", pluginName))
		}
	default:
		errs = append(errs, fmt.Errorf("%s: action %q is not supported — valid actions are \"add\" and \"remove\" (\"remove\" requires legacy v3 credentials)", pluginName, action))
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, "api_key", "user_pin", "action", "legacy_user_key", "legacy_user_name")...)
	return errs
}

type tvdbFavoritesSink struct {
	client *itvdb.Client
	remove bool
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("%s: api_key is required", pluginName)
	}
	userPin, _ := cfg["user_pin"].(string)
	if userPin == "" {
		return nil, fmt.Errorf("%s: user_pin is required", pluginName)
	}
	action, _ := cfg["action"].(string)
	legacyKey, _ := cfg["legacy_user_key"].(string)
	legacyName, _ := cfg["legacy_user_name"].(string)
	switch action {
	case "", "add":
		// ok
	case "remove":
		if legacyKey == "" || legacyName == "" {
			return nil, fmt.Errorf("%s: action \"remove\" requires legacy_user_key and legacy_user_name (thetvdb's v4 api cannot remove favorites; removal uses the legacy v3 api)", pluginName)
		}
	default:
		return nil, fmt.Errorf("%s: action %q is not supported — valid actions are \"add\" and \"remove\"", pluginName, action)
	}
	client := itvdb.NewWithPin(apiKey, userPin)
	if legacyKey != "" && legacyName != "" {
		client.WithLegacyAuth(legacyKey, legacyName)
	}
	return &tvdbFavoritesSink{
		client: client,
		remove: action == "remove",
	}, nil
}

func (p *tvdbFavoritesSink) Name() string { return pluginName }

func (p *tvdbFavoritesSink) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	verb := "add"
	if p.remove {
		verb = "remove"
	}

	if tc.DryRun {
		for _, e := range entries {
			id, ok := tvdbID(e)
			if !ok {
				e.Fail(pluginName + ": entry has no usable tvdb_id")
				continue
			}
			e.Accept(fmt.Sprintf("%s: would %s favorite %d", pluginName, verb, id))
			tc.Logger.Info(pluginName+": dry-run, would "+verb+" favorite", "title", e.Title, "tvdb_id", id)
		}
		return nil
	}

	// Fetch the current favorites once per run so redundant operations are
	// consumed silently: already-favorited series are not re-added, and
	// series not in the favorites are not removed.
	existing := make(map[int]bool)
	if ids, err := p.client.GetFavorites(ctx); err != nil {
		tc.Logger.Warn(pluginName+": could not fetch existing favorites; "+verb+"s may be redundant", "err", err)
	} else {
		for _, id := range ids {
			existing[id] = true
		}
	}

	for _, e := range entries {
		id, ok := tvdbID(e)
		if !ok {
			e.Fail(pluginName + ": entry has no usable tvdb_id")
			continue
		}
		if p.remove {
			if !existing[id] {
				tc.Logger.Debug(pluginName+": not in favorites", "title", e.Title, "tvdb_id", id)
				e.Consume()
				continue
			}
			if err := p.client.RemoveFavorite(ctx, id); err != nil {
				tc.Logger.Warn(pluginName+": remove favorite failed", "title", e.Title, "tvdb_id", id, "err", err)
				e.Fail(pluginName + ": " + err.Error())
				continue
			}
			tc.Logger.Info(pluginName+": removed favorite", "title", e.Title, "tvdb_id", id)
			continue
		}
		if existing[id] {
			tc.Logger.Debug(pluginName+": already a favorite", "title", e.Title, "tvdb_id", id)
			e.Consume()
			continue
		}
		if err := p.client.AddFavorite(ctx, id); err != nil {
			tc.Logger.Warn(pluginName+": add favorite failed", "title", e.Title, "tvdb_id", id, "err", err)
			e.Fail(pluginName + ": " + err.Error())
			continue
		}
		tc.Logger.Info(pluginName+": added favorite", "title", e.Title, "tvdb_id", id)
	}
	return nil
}

// tvdbID extracts the tvdb_id field as a positive int. The field is a string
// when set by tvdb_favorites/metainfo_tvdb but tolerate numeric types too.
func tvdbID(e *entry.Entry) (int, bool) {
	v, ok := e.Get("tvdb_id")
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, n > 0
	case int64:
		return int(n), n > 0
	case float64:
		return int(n), int(n) > 0
	case string:
		id, err := strconv.Atoi(n)
		return id, err == nil && id > 0
	}
	return 0, false
}
