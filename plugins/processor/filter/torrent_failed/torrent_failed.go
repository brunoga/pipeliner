// Package torrent_failed provides a classifier that accepts torrents whose
// grab has failed — errored in the client, or stalled/zero-progress for
// longer than stall_timeout — and rejects healthy ones.
//
// Upstream is the torrent_session source. Typical chain:
//
//	sess   = input("torrent_session", backend="transmission")
//	failed = process("torrent_failed", upstream=sess, stall_timeout="4h")
//	marked = output("mark_failed", upstream=failed)
//	output("torrent_control", upstream=marked,
//	       action="remove_with_data", backend="transmission")
//
// Classification rules, in order:
//
//   - torrent_state == "errored"                          → accepted (failed)
//   - torrent_state == "stalled", or "downloading" with
//     torrent_progress == 0: accepted once the torrent has
//     shown no activity for stall_timeout (measured from
//     torrent_last_activity, falling back to
//     torrent_added_at)                                   → accepted (failed)
//   - everything else (seeding, paused, checking, healthy
//     or recently-active downloads)                       → rejected
//
// A slow-but-moving download keeps refreshing its last-activity timestamp,
// so it is never classified as failed no matter how long it takes.
//
// Config keys:
//
//	stall_timeout - how long a stalled/zero-progress torrent may sit without
//	                activity before it counts as failed (default: "4h")
package torrent_failed

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentclient"
)

const pluginName = "torrent_failed"

const defaultStallTimeout = 4 * time.Hour

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "accept dead torrents (errored, or stalled longer than stall_timeout); reject healthy ones",
		Role:        plugin.RoleProcessor,
		Requires:    plugin.RequireAll(entry.FieldTorrentState),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "stall_timeout", Type: plugin.FieldTypeDuration, Default: "4h", Hint: "Inactivity window before a stalled/zero-progress torrent counts as failed"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptDuration(cfg, "stall_timeout", pluginName); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, "stall_timeout")...)
	return errs
}

type failedPlugin struct {
	stallTimeout time.Duration
	// now is the reference time; overridable in tests.
	now func() time.Time
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	stallTimeout := defaultStallTimeout
	if v, _ := cfg["stall_timeout"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid stall_timeout %q: %w", pluginName, v, err)
		}
		stallTimeout = d
	}
	return &failedPlugin{stallTimeout: stallTimeout, now: time.Now}, nil
}

func (p *failedPlugin) Name() string { return pluginName }

func (p *failedPlugin) Process(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		p.classify(tc, e)
	}
	return entries, nil
}

func (p *failedPlugin) classify(tc *plugin.TaskContext, e *entry.Entry) {
	state := e.GetString(entry.FieldTorrentState)

	if state == string(torrentclient.StateErrored) {
		msg := e.GetString(entry.FieldTorrentError)
		if msg == "" {
			msg = "client reported an error"
		}
		e.Accept(fmt.Sprintf("%s: errored: %s", pluginName, msg))
		tc.Logger.Info(pluginName+": failed torrent", "torrent", e.Title, "cause", "errored", "error", msg)
		return
	}

	stalled := state == string(torrentclient.StateStalled)
	zeroProgress := state == string(torrentclient.StateDownloading) &&
		getFloat(e, entry.FieldTorrentProgress) == 0
	if !stalled && !zeroProgress {
		e.Reject(fmt.Sprintf("%s: healthy (%s)", pluginName, state))
		return
	}

	// Inactivity is measured from the client's last-activity timestamp; a
	// torrent that never transferred anything falls back to its add time.
	ref := e.GetTime(entry.FieldTorrentLastActivity)
	if ref.IsZero() {
		ref = e.GetTime(entry.FieldTorrentAddedAt)
	}
	if ref.IsZero() {
		// No timing information at all — err on the side of keeping it.
		e.Reject(fmt.Sprintf("%s: %s but no activity/added timestamps to judge staleness", pluginName, state))
		return
	}

	inactive := p.now().Sub(ref)
	if inactive < p.stallTimeout {
		e.Reject(fmt.Sprintf("%s: %s for %s, below stall_timeout %s",
			pluginName, state, inactive.Round(time.Minute), p.stallTimeout))
		return
	}

	e.Accept(fmt.Sprintf("%s: %s with no activity for %s (stall_timeout %s)",
		pluginName, state, inactive.Round(time.Minute), p.stallTimeout))
	tc.Logger.Info(pluginName+": failed torrent", "torrent", e.Title, "cause", state, "inactive", inactive)
}

// getFloat reads a numeric field as float64 (0 when absent or non-numeric).
func getFloat(e *entry.Entry, key string) float64 {
	v, ok := e.Get(key)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
