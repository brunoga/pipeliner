// Package tvdb provides an input plugin that fetches shows from a TheTVDB
// user's favorites list and emits one entry per show.
//
// Entries carry the show name and a canonical TheTVDB URL. They are suitable
// as title sources for discover.from and series.from.
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
		PluginName:  "input_tvdb",
		Description: "fetch shows from a TheTVDB user's favorites list as pipeline entries",
		PluginPhase: plugin.PhaseInput,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "api_key", "input_tvdb"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "user_pin", "input_tvdb"); err != nil {
		errs = append(errs, err)
	}
	return errs
}

type tvdbInputPlugin struct {
	client *itvdb.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("input_tvdb: api_key is required")
	}
	userPin, _ := cfg["user_pin"].(string)
	if userPin == "" {
		return nil, fmt.Errorf("input_tvdb: user_pin is required")
	}
	return &tvdbInputPlugin{
		client: itvdb.NewWithPin(apiKey, userPin),
	}, nil
}

func (p *tvdbInputPlugin) Name() string        { return "input_tvdb" }
func (p *tvdbInputPlugin) Phase() plugin.Phase { return plugin.PhaseInput }

func (p *tvdbInputPlugin) Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	ids, err := p.client.GetFavorites(ctx)
	if err != nil {
		return nil, fmt.Errorf("input_tvdb: get favorites: %w", err)
	}

	entries := make([]*entry.Entry, 0, len(ids))
	for _, id := range ids {
		s, err := p.client.GetSeriesByID(ctx, id)
		if err != nil {
			tc.Logger.Warn("input_tvdb: GetSeriesByID failed", "id", id, "err", err)
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
