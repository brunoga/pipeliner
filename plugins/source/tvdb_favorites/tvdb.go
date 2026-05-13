// Package tvdb provides a list sub-plugin that fetches shows from a TheTVDB
// user's favorites list and emits one entry per show.
//
// Usable as a standalone DAG source node (input()) or as a list= entry for
// series or discover to supply a dynamic show list.
//
// Config keys:
//
//	api_key  - TheTVDB API key (required)
//	user_pin - User PIN from thetvdb.com (required; enables favorites access)
package tvdb

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "tvdb_favorites",
		Description: "fetch TheTVDB favorites as show-name entries; usable as a standalone DAG source or inside series.from/discover.from",
		Role:        plugin.RoleSource,
		Produces:    []string{entry.FieldTitle, "tvdb_id", "tvdb_year"},
		Factory:      newPlugin,
		Validate:     validate,
		IsListPlugin: true,
		Schema: []plugin.FieldSchema{
			{Key: "api_key",  Type: plugin.FieldTypeString, Required: true, Hint: "TheTVDB v4 API key"},
			{Key: "user_pin", Type: plugin.FieldTypeString, Required: true, Hint: "TheTVDB user PIN"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "api_key", "tvdb_favorites"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "user_pin", "tvdb_favorites"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "tvdb_favorites", "api_key", "user_pin")...)
	return errs
}

type tvdbSourcePlugin struct {
	client *itvdb.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("tvdb_favorites: api_key is required")
	}
	userPin, _ := cfg["user_pin"].(string)
	if userPin == "" {
		return nil, fmt.Errorf("tvdb_favorites: user_pin is required")
	}
	return &tvdbSourcePlugin{
		client: itvdb.NewWithPin(apiKey, userPin),
	}, nil
}

func (p *tvdbSourcePlugin) Name() string        { return "tvdb_favorites" }

func (p *tvdbSourcePlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	ids, err := p.client.GetFavorites(ctx)
	if err != nil {
		return nil, fmt.Errorf("tvdb_favorites: get favorites: %w", err)
	}

	entries := make([]*entry.Entry, 0, len(ids))
	for _, id := range ids {
		s, err := p.client.GetSeriesByID(ctx, id)
		if err != nil {
			tc.Logger.Warn("tvdb_favorites: GetSeriesByID failed", "id", id, "err", err)
			continue
		}
		if s == nil || s.Name == "" {
			continue
		}
		url := fmt.Sprintf("https://thetvdb.com/series/%s", s.Slug)
		e := entry.New(s.Name, url)
		e.Set("tvdb_id", s.ID)
		if s.Year != "" {
			e.Set("tvdb_year", s.Year)
		}
		entries = append(entries, e)
	}
	return entries, nil
}
