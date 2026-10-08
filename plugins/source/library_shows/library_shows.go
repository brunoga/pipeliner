// Package library_shows provides a source plugin that emits one entry per TV
// show present in a media server's library.
//
// It answers a different question from series_tracker, which emits the shows
// pipeliner has download records for. This one emits the shows you actually
// have — including every show acquired some other way, which the tracker has
// never heard of. Feed it into series_gaps to backfill from library truth,
// or use it as a list= source.
//
// Pair it with series_gaps(backend=…, seasons="from_first_owned") and the
// whole chain is answered by the library: which shows to consider, which
// episodes are missing, and how far back to go.
//
// Entry shape:
//
//	Title                 the show title as the server reports it
//	URL                   pipeliner://series/<normalized-name> (stable, for dedup)
//	series_name           normalized name, the key series_gaps looks up by
//	series_episode_count  episodes of the show held by the server
//	media_type            series
//
// Config keys:
//
//	backend          - "plex" or "jellyfin" (default: "plex")
//	url, token       - server address and token; omit both for Plex account
//	                   mode (sign in on the Tools tab)
//	sections         - only read these server libraries, by name
//	exclude_sections - read every library except these
package library_shows

import (
	"context"
	"fmt"
	"net/url"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/mediaserver"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "library_shows"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "emit one entry per TV show present in a Plex/Jellyfin library; usable as a standalone DAG source or inside list=",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTitle,
			entry.FieldSource,
			entry.FieldMediaType,
			entry.FieldSeriesName,
			entry.FieldSeriesEpisodeCount,
		},
		Schema: []plugin.FieldSchema{
			{Key: "backend", Type: plugin.FieldTypeString, Default: "plex", Hint: "Media server: plex or jellyfin"},
			{Key: "url", Type: plugin.FieldTypeString, Hint: "Media server base URL; omit for Plex account mode (sign in on the Tools tab)"},
			{Key: "token", Type: plugin.FieldTypeString, Hint: "Media server API token; omit for Plex account mode"},
			{Key: "sections", Type: plugin.FieldTypeList, Hint: "Only read these server libraries, by name (e.g. [\"TV Shows\"])"},
			{Key: "exclude_sections", Type: plugin.FieldTypeList, Hint: "Read every server library except these, by name"},
		},
		Factory:      newPlugin,
		Validate:     validate,
		IsListPlugin: true,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if b, _ := cfg["backend"].(string); b != "" && b != "plex" && b != "jellyfin" {
		errs = append(errs, fmt.Errorf("%s: backend %q is not plex or jellyfin", pluginName, b))
	}
	if len(plugin.ToStringSlice(cfg["sections"])) > 0 && len(plugin.ToStringSlice(cfg["exclude_sections"])) > 0 {
		errs = append(errs, fmt.Errorf("%s: set 'sections' or 'exclude_sections', not both", pluginName))
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"backend", "url", "token", "sections", "exclude_sections")...)
	return errs
}

type showsSourcePlugin struct {
	client   mediaserver.Client
	sections mediaserver.Sections
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	backend, _ := cfg["backend"].(string)
	if backend == "" {
		backend = "plex"
	}
	serverURL, _ := cfg["url"].(string)
	token, _ := cfg["token"].(string)

	var bucket store.Bucket
	if db != nil {
		bucket = db.Bucket(mediaserver.PlexSettingsBucket)
	}
	client, err := mediaserver.Connect(backend, serverURL, token, bucket)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pluginName, err)
	}

	return &showsSourcePlugin{
		client: client,
		sections: mediaserver.NewSections(
			plugin.ToStringSlice(cfg["sections"]),
			plugin.ToStringSlice(cfg["exclude_sections"])),
	}, nil
}

func (p *showsSourcePlugin) Name() string { return pluginName }

func (p *showsSourcePlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	// An error is returned rather than logged-and-skipped: emitting nothing
	// would look exactly like an empty library, and a downstream backfill
	// would then quietly do nothing while appearing to succeed.
	owned, err := mediaserver.BuildOwnedEpisodes(ctx, p.client, p.sections, match.Normalize)
	if err != nil {
		return nil, fmt.Errorf("%s: read library: %w", pluginName, err)
	}

	names := owned.Shows()
	entries := make([]*entry.Entry, 0, len(names))
	for _, norm := range names {
		// Synthetic stable URL, matching series_tracker's shape so the two
		// sources dedup against each other when merged.
		e := entry.New(owned.Title(norm), "pipeliner://series/"+url.PathEscape(norm))
		e.Set(entry.FieldSource, pluginName)
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		e.Set(entry.FieldSeriesName, norm)
		e.Set(entry.FieldSeriesEpisodeCount, owned.EpisodeCount(norm))
		entries = append(entries, e)
	}
	tc.Logger.Debug(pluginName+": generated library shows",
		"shows", len(entries), "episodes", owned.Count())
	return entries, nil
}
