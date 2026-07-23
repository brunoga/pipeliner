// Package torrent_control provides a sink that acts on torrents in a
// download client's session: remove, remove with data, pause, or force a
// tracker reannounce. It pairs with the torrent_session source, which emits
// the torrent_info_hash field this sink requires.
//
// WARNING: action="remove_with_data" deletes the torrent's downloaded files
// from disk, not just the session entry. There is no undo.
//
// Config keys mirror torrent_session:
//
//	action   - "remove", "remove_with_data", "pause", or "reannounce" (required)
//	backend  - "transmission", "qbittorrent", or "deluge" (required)
//	host     - client host (default: "localhost")
//	port     - client port (default: 9091 transmission, 8080 qbittorrent, 8112 deluge)
//	username - auth username (optional)
//	password - auth password (optional)
//	rpc_path - Transmission RPC endpoint path (default: "/transmission/rpc")
//	tls      - use HTTPS (qBittorrent only)
package torrent_control

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentclient"
)

const pluginName = "torrent_control"

const (
	actionRemove         = "remove"
	actionRemoveWithData = "remove_with_data"
	actionPause          = "pause"
	actionReannounce     = "reannounce"
)

var actions = []string{actionRemove, actionRemoveWithData, actionPause, actionReannounce}

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "remove, pause, or reannounce torrents in a Transmission or qBittorrent session (pairs with the torrent_session source)",
		Role:        plugin.RoleSink,
		Requires:    plugin.RequireAll(entry.FieldTorrentInfoHash),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "action", Type: plugin.FieldTypeEnum, Enum: actions, Required: true, Hint: "remove_with_data also deletes the downloaded files from disk"},
			{Key: "backend", Type: plugin.FieldTypeEnum, Enum: torrentclient.Backends, Required: true, Hint: "Download client to control"},
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
	if err := plugin.RequireString(cfg, "action", pluginName); err != nil {
		errs = append(errs, err)
	} else if err := plugin.OptEnum(cfg, "action", pluginName, actions...); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "backend", pluginName); err != nil {
		errs = append(errs, err)
	} else if err := plugin.OptEnum(cfg, "backend", pluginName, torrentclient.Backends...); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"action", "backend", "host", "port", "username", "password", "rpc_path", "tls")...)
	return errs
}

type controlSink struct {
	action string
	client torrentclient.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	action, _ := cfg["action"].(string)
	valid := false
	for _, a := range actions {
		if action == a {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("%s: action must be one of %s, got %q",
			pluginName, strings.Join(actions, ", "), action)
	}
	backend, _ := cfg["backend"].(string)
	client, err := torrentclient.New(backend, torrentclient.ConfigFromMap(cfg))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pluginName, err)
	}
	return &controlSink{action: action, client: client}, nil
}

func (p *controlSink) Name() string { return pluginName }

// verb returns a human-readable description of the action for reasons/logs.
func (p *controlSink) verb() string {
	switch p.action {
	case actionRemove:
		return "remove"
	case actionRemoveWithData:
		return "remove (with data)"
	case actionPause:
		return "pause"
	case actionReannounce:
		return "reannounce"
	}
	return p.action
}

// done returns the past-tense form of the action for accept reasons.
func (p *controlSink) done() string {
	switch p.action {
	case actionRemove:
		return "removed"
	case actionRemoveWithData:
		return "removed (with data)"
	case actionPause:
		return "paused"
	case actionReannounce:
		return "reannounced"
	}
	return p.action
}

func (p *controlSink) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	// Collect the hash per entry up front so dry-run and live paths report
	// missing hashes identically.
	var hashes []string
	var acting []*entry.Entry
	for _, e := range entries {
		hash := strings.ToLower(e.GetString(entry.FieldTorrentInfoHash))
		if hash == "" {
			e.Fail(pluginName + ": entry has no torrent_info_hash")
			continue
		}
		if tc.DryRun {
			e.Accept(fmt.Sprintf("%s: would %s %s (%s)", pluginName, p.verb(), e.Title, hash))
			tc.Logger.Info(pluginName+": dry-run", "action", p.action, "torrent", e.Title, "hash", hash)
			continue
		}
		hashes = append(hashes, hash)
		acting = append(acting, e)
	}
	if tc.DryRun || len(hashes) == 0 {
		return nil
	}

	var err error
	switch p.action {
	case actionRemove:
		err = p.client.Remove(ctx, hashes, false)
	case actionRemoveWithData:
		err = p.client.Remove(ctx, hashes, true)
	case actionPause:
		err = p.client.Pause(ctx, hashes)
	case actionReannounce:
		err = p.client.Reannounce(ctx, hashes)
	}
	if err != nil {
		for _, e := range acting {
			e.Fail(fmt.Sprintf("%s: %s: %v", pluginName, p.verb(), err))
		}
		return fmt.Errorf("%s: %s: %w", pluginName, p.verb(), err)
	}
	for _, e := range acting {
		e.Accept(fmt.Sprintf("%s: %s %s", pluginName, p.done(), e.Title))
		tc.Logger.Info(pluginName+": done", "action", p.action, "torrent", e.Title)
	}
	return nil
}
