// Package torrent_session provides a source plugin that emits one entry per
// torrent in a download client's session, so janitor pipelines can inspect
// ratio/seed-time/state and act via the torrent_control and mark_failed
// sinks.
//
// Entry shape:
//
//	Title                   torrent display name
//	URL                     torrent://<info-hash> (stable identifier for dedup
//	                        and cross-branch matching)
//	torrent_info_hash       lowercase hex info-hash
//	torrent_state           downloading|seeding|stalled|paused|errored|checking
//	torrent_ratio           upload ratio (float)
//	torrent_seed_time       cumulative seeding time in seconds
//	torrent_added_at        when the torrent was added
//	torrent_progress        completion percentage, 0-100
//	torrent_download_dir    data directory
//	torrent_error           client error message (only when state is errored)
//	torrent_last_activity   last transfer activity (only when the client knows)
//
// Config keys mirror the corresponding sink plugin's connection keys:
//
//	backend  - "transmission", "qbittorrent", or "deluge" (required)
//	host     - client host (default: "localhost")
//	port     - client port (default: 9091 transmission, 8080 qbittorrent, 8112 deluge)
//	username - auth username (optional)
//	password - auth password (optional)
//	rpc_path - Transmission RPC endpoint path (default: "/transmission/rpc")
//	tls      - use HTTPS (qBittorrent only)
package torrent_session

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentclient"
)

const pluginName = "torrent_session"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "emit one entry per torrent in a Transmission or qBittorrent session, with ratio/seed-time/state fields for janitor pipelines",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTitle,
			entry.FieldSource,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentState,
			entry.FieldTorrentRatio,
			entry.FieldTorrentSeedTime,
			entry.FieldTorrentAddedAt,
			entry.FieldTorrentProgress,
			entry.FieldTorrentDownloadDir,
		},
		MayProduce: []string{
			entry.FieldTorrentError,
			entry.FieldTorrentLastActivity,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "backend", Type: plugin.FieldTypeEnum, Enum: torrentclient.Backends, Required: true, Hint: "Download client to query"},
			{Key: "host", Type: plugin.FieldTypeString, Default: "localhost", Hint: "Client host"},
			{Key: "port", Type: plugin.FieldTypeInt, Hint: "Client port (default: 9091 transmission, 8080 qbittorrent, 8112 deluge)"},
			{Key: "username", Type: plugin.FieldTypeString, Hint: "Auth username"},
			{Key: "password", Type: plugin.FieldTypeString, Hint: "Auth password"},
			{Key: "rpc_path", Type: plugin.FieldTypeString, Default: "/transmission/rpc", Hint: "Transmission RPC endpoint path"},
			{Key: "tls", Type: plugin.FieldTypeBool, Hint: "Use HTTPS (qBittorrent only)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "backend", pluginName); err != nil {
		errs = append(errs, err)
	} else if err := plugin.OptEnum(cfg, "backend", pluginName, torrentclient.Backends...); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"backend", "host", "port", "username", "password", "rpc_path", "tls")...)
	return errs
}

type sessionSourcePlugin struct {
	backend string
	client  torrentclient.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	backend, _ := cfg["backend"].(string)
	client, err := torrentclient.New(backend, torrentclient.ConfigFromMap(cfg))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pluginName, err)
	}
	return &sessionSourcePlugin{backend: backend, client: client}, nil
}

func (p *sessionSourcePlugin) Name() string { return pluginName }

func (p *sessionSourcePlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	torrents, err := p.client.ListTorrents(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: list torrents: %w", pluginName, err)
	}

	entries := make([]*entry.Entry, 0, len(torrents))
	for _, t := range torrents {
		// torrent:// + info-hash is stable across runs and clients, so dedup
		// and cross-branch URL matching work.
		e := entry.New(t.Name, "torrent://"+t.Hash)
		e.Set(entry.FieldSource, pluginName+":"+p.backend)
		e.Set(entry.FieldTorrentInfoHash, t.Hash)
		e.Set(entry.FieldTorrentState, string(t.State))
		e.Set(entry.FieldTorrentRatio, t.Ratio)
		e.Set(entry.FieldTorrentSeedTime, int64(t.SeedTime.Seconds()))
		e.Set(entry.FieldTorrentAddedAt, t.AddedAt)
		e.Set(entry.FieldTorrentProgress, t.Progress)
		e.Set(entry.FieldTorrentDownloadDir, t.DownloadDir)
		if t.Error != "" {
			e.Set(entry.FieldTorrentError, t.Error)
		}
		if !t.LastActivity.IsZero() {
			e.Set(entry.FieldTorrentLastActivity, t.LastActivity)
		}
		entries = append(entries, e)
	}
	tc.Logger.Debug(pluginName+": listed session torrents", "backend", p.backend, "count", len(entries))
	return entries, nil
}
