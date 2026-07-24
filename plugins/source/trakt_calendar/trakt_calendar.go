// Package trakt_calendar emits one entry per upcoming episode from the
// authenticated Trakt user's "my shows" calendar — the shows Trakt considers
// followed (watching/collected/watchlisted). The Trakt-native counterpart to
// tvdb_calendar (which is driven by the local series tracker).
package trakt_calendar

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

const pluginName = "trakt_calendar"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "emit one entry per upcoming episode (within window) from the Trakt my-shows calendar",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTitle, entry.FieldMediaType,
			entry.FieldSeriesEpisodeID, entry.FieldSeriesSeason, entry.FieldSeriesEpisode,
			entry.FieldSeriesAirDate,
		},
		MayProduce: []string{"tvdb_id", "trakt_id", entry.FieldDescription},
		Factory:    newPlugin,
		Validate:   validate,
		Schema: []plugin.FieldSchema{
			{Key: "client_id", Type: plugin.FieldTypeString, Required: true, Hint: "Trakt API client ID"},
			{Key: "client_secret", Type: plugin.FieldTypeString, Hint: "OAuth client secret (uses the stored token from `pipeliner auth trakt`)"},
			{Key: "access_token", Type: plugin.FieldTypeString, Hint: "Static OAuth bearer token (alternative to client_secret)"},
			{Key: "window", Type: plugin.FieldTypeDuration, Default: "24h", Hint: "How far ahead to look for upcoming episodes"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptUnknownKeys(cfg, pluginName, "client_id", "client_secret", "access_token", "window"); err != nil {
		errs = append(errs, err...)
	}
	if id, _ := cfg["client_id"].(string); id == "" {
		errs = append(errs, fmt.Errorf("%s: 'client_id' is required", pluginName))
	}
	cs, _ := cfg["client_secret"].(string)
	at, _ := cfg["access_token"].(string)
	if cs == "" && at == "" {
		errs = append(errs, fmt.Errorf("%s: the calendar is account-specific — set 'client_secret' (stored oauth token) or 'access_token'", pluginName))
	}
	if err := plugin.OptDuration(cfg, "window", pluginName); err != nil {
		errs = append(errs, err)
	}
	return errs
}

type calendarPlugin struct {
	clientID     string
	clientSecret string
	staticToken  string
	authBucket   store.Bucket
	window       time.Duration
	now          func() time.Time
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	id, _ := cfg["client_id"].(string)
	if id == "" {
		return nil, fmt.Errorf("%s: 'client_id' is required", pluginName)
	}
	window := 24 * time.Hour
	if s, ok := cfg["window"].(string); ok && s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid window: %w", pluginName, err)
		}
		window = d
	}
	p := &calendarPlugin{
		clientID: id,
		window:   window,
		now:      time.Now,
	}
	p.clientSecret, _ = cfg["client_secret"].(string)
	p.staticToken, _ = cfg["access_token"].(string)
	if db != nil && p.clientSecret != "" {
		p.authBucket = db.Bucket(itrakt.AuthBucket)
	}
	return p, nil
}

func (p *calendarPlugin) Name() string { return pluginName }

func (p *calendarPlugin) buildClient(ctx context.Context) (*itrakt.Client, error) {
	if p.authBucket != nil {
		token, err := itrakt.GetValidAccessToken(ctx, p.authBucket, p.clientID, p.clientSecret)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pluginName, err)
		}
		return itrakt.NewWithToken(p.clientID, token), nil
	}
	return itrakt.NewWithToken(p.clientID, p.staticToken), nil
}

// Generate implements plugin.SourcePlugin.
func (p *calendarPlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	client, err := p.buildClient(ctx)
	if err != nil {
		return nil, err
	}
	now := p.now()
	horizon := now.Add(p.window)
	days := int(math.Ceil(p.window.Hours()/24)) + 1

	cal, err := client.MyCalendarShows(ctx, now, days)
	if err != nil {
		return nil, err
	}

	var out []*entry.Entry
	for _, ce := range cal {
		if ce.FirstAired.Before(now) || ce.FirstAired.After(horizon) {
			continue
		}
		epID := fmt.Sprintf("S%02dE%02d", ce.Episode.Season, ce.Episode.Number)
		e := entry.New(
			fmt.Sprintf("%s %s", ce.Show.Title, epID),
			fmt.Sprintf("pipeliner://trakt-calendar/%s/%s", url.PathEscape(ce.Show.Title), epID),
		)
		e.Fields[entry.FieldMediaType] = "series"
		e.Fields[entry.FieldSeriesEpisodeID] = epID
		e.Fields[entry.FieldSeriesSeason] = ce.Episode.Season
		e.Fields[entry.FieldSeriesEpisode] = ce.Episode.Number
		e.Fields[entry.FieldSeriesAirDate] = ce.FirstAired
		if ce.Episode.Title != "" {
			e.Fields[entry.FieldDescription] = ce.Episode.Title
		}
		if ce.Show.IDs.TVDB > 0 {
			e.Fields["tvdb_id"] = ce.Show.IDs.TVDB
		}
		if ce.Show.IDs.Trakt > 0 {
			e.Fields["trakt_id"] = ce.Show.IDs.Trakt
		}
		out = append(out, e)
	}
	tc.Logger.Info(pluginName+": upcoming episodes", "count", len(out), "window", p.window.String())
	return out, nil
}
