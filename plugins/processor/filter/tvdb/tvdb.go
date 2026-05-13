// Package tvdb implements a filter plugin that fetches a user's TheTVDB favorites
// and accepts entries whose parsed series name matches a favorited show.
//
// The favorites list is cached and refreshed according to the ttl setting
// (default 1h).
//
// Config keys:
//
//	api_key   - TheTVDB API key (required)
//	user_pin  - User PIN from thetvdb.com (required; enables favorites access)
//	ttl       - cache lifetime, e.g. "1h", "30m" (default: "1h")
package tvdb

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	iseries "github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "tvdb",
		Role:        plugin.RoleProcessor,
		Description: "Accept entries whose series name appears in the user's TheTVDB favorites",
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "api_key",          Type: plugin.FieldTypeString,   Required: true, Hint: "TheTVDB v4 API key"},
			{Key: "user_pin",         Type: plugin.FieldTypeString,   Required: true, Hint: "TheTVDB user PIN"},
			{Key: "ttl",              Type: plugin.FieldTypeDuration,                 Hint: "Favorites cache lifetime (default 1h)"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool,                     Hint: "Reject entries not in favorites (default true)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "api_key", "tvdb"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "user_pin", "tvdb"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "ttl", "tvdb"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "tvdb", "api_key", "user_pin", "ttl", "reject_unmatched")...)
	return errs
}

const cacheKey = "favorites"

type tvdbFilter struct {
	client          *itvdb.Client
	cache           *cache.Cache[[]string]
	rejectUnmatched bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("tvdb filter: api_key is required")
	}

	userPin, _ := cfg["user_pin"].(string)
	if userPin == "" {
		return nil, fmt.Errorf("tvdb filter: user_pin is required")
	}

	ttl := time.Hour
	if v, _ := cfg["ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("tvdb filter: invalid ttl %q: %w", v, err)
		}
		ttl = d
	}

	rejectUnmatched := true
	if v, ok := cfg["reject_unmatched"]; ok {
		rejectUnmatched, _ = v.(bool)
	}
	return &tvdbFilter{
		client:          itvdb.NewWithPin(apiKey, userPin),
		cache:           cache.NewPersistent[[]string](ttl, db.Bucket("cache_filter_tvdb")),
		rejectUnmatched: rejectUnmatched,
	}, nil
}

func (p *tvdbFilter) Name() string        { return "tvdb" }

func (p *tvdbFilter) filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	titles, err := p.ensureTitles(ctx, tc)
	if err != nil {
		tc.Logger.Warn("tvdb: could not fetch favorites, skipping filter", "err", err)
		return nil
	}

	ep, ok := iseries.Parse(e.Title)
	if !ok {
		if p.rejectUnmatched {
			e.Reject("tvdb: title did not parse as episode")
		}
		return nil
	}

	norm := match.Normalize(ep.SeriesName)
	for _, t := range titles {
		if match.Fuzzy(norm, t) {
			e.Accept()
			return nil
		}
	}
	if p.rejectUnmatched {
		e.Reject("tvdb: show not in favorites")
	}
	return nil
}

// ensureTitles returns the cached titles, fetching from TheTVDB if stale.
func (p *tvdbFilter) ensureTitles(ctx context.Context, tc *plugin.TaskContext) ([]string, error) {
	if titles, ok := p.cache.Get(cacheKey); ok {
		return titles, nil
	}

	ids, err := p.client.GetFavorites(ctx)
	if err != nil {
		return nil, err
	}

	titles := make([]string, 0, len(ids))
	for _, id := range ids {
		s, err := p.client.GetSeriesByID(ctx, id)
		if err != nil {
			tc.Logger.Warn("tvdb: GetSeriesByID failed", "id", id, "err", err)
			continue
		}
		if s.Name != "" {
			titles = append(titles, match.Normalize(s.Name))
		}
	}

	if len(titles) > 0 {
		p.cache.Set(cacheKey, titles)
	}
	return titles, nil
}

func (p *tvdbFilter) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}
