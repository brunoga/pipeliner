// Package torrent_in_use rejects files that a download client's torrent
// still uses.
//
// It is the guard in front of an age-based file sweep. Deleting a file a
// torrent is seeding from stops the seeding, and on a tracker that enforces a
// share ratio that costs the account: the sweep must leave those files to
// whatever removes the torrent (torrent_control with remove_with_data).
//
// A torrent uses everything below its download directory joined with its
// name: the file of a single-file torrent, the folder of a multi-file one.
// The client is asked once per run. When it cannot be asked, every entry is
// rejected: deleting a file that might be seeding is the one mistake the
// guard exists to prevent, so not knowing means not deleting.
package torrent_in_use

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentclient"
)

const pluginName = "torrent_in_use"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "reject files a download client's torrent still uses, so a cleanup cannot stop a torrent seeding",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalPerItem,
		Requires:    plugin.RequireAll(entry.FieldFileLocation),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "backend", Type: plugin.FieldTypeEnum, Enum: torrentclient.Backends, Required: true, Hint: "Download client to ask"},
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

type inUsePlugin struct {
	client torrentclient.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	backend, _ := cfg["backend"].(string)
	client, err := torrentclient.New(backend, torrentclient.ConfigFromMap(cfg))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pluginName, err)
	}
	return &inUsePlugin{client: client}, nil
}

func (p *inUsePlugin) Name() string { return pluginName }

func (p *inUsePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	torrents, err := p.client.ListTorrents(ctx)
	if err != nil {
		tc.Logger.Warn(pluginName+": download client unreachable, keeping every file this run", "err", err)
		for _, e := range entries {
			e.Reject(fmt.Sprintf("%s: could not ask the download client which files are in use (%v), so nothing is treated as free", pluginName, err))
		}
		return entry.PassThrough(entries), nil
	}
	roots := make([]owned, 0, len(torrents))
	for _, t := range torrents {
		if t.DownloadDir == "" || t.Name == "" {
			continue
		}
		roots = append(roots, owned{path: filepath.Clean(filepath.Join(t.DownloadDir, t.Name)), name: t.Name})
	}
	for _, e := range entries {
		loc := e.GetString(entry.FieldFileLocation)
		if loc == "" {
			e.Reject(pluginName + ": no file_location to check")
			continue
		}
		if o, ok := ownerOf(filepath.Clean(loc), roots); ok {
			e.Reject(fmt.Sprintf("%s: torrent %q still uses this file", pluginName, o.name))
		}
	}
	return entry.PassThrough(entries), nil
}

type owned struct{ path, name string }

// ownerOf returns the torrent whose data holds path: the path itself, or a
// folder above it.
func ownerOf(path string, roots []owned) (owned, bool) {
	for _, o := range roots {
		if path == o.path || strings.HasPrefix(path, o.path+string(filepath.Separator)) {
			return o, true
		}
	}
	return owned{}, false
}
