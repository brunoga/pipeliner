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
//   - torrent_progress >= 100                             → rejected (healthy)
//   - torrent_state == "errored"                          → accepted (failed)
//   - torrent_state == "stalled" or "downloading": accepted
//     once the torrent has shown no activity for stall_timeout
//     (measured from torrent_last_activity, falling back to
//     torrent_added_at)                                   → accepted (failed)
//   - with max_eta set, a stalled or downloading torrent that has been
//     watched for a whole slow_window and, at the best of its lifetime,
//     recent and current rates, would still need more than max_eta to
//     finish                                              → accepted (failed)
//   - everything else (seeding, paused, checking, healthy
//     or recently-active downloads)                       → rejected
//
// The inactivity window — not the absolute progress — decides. A slow-but-
// moving download keeps refreshing its last-activity timestamp, so it is never
// classified as failed no matter how long it takes; a download that reached,
// say, 50% and then stalled with no activity for stall_timeout is failed just
// like one stuck at 0%. (Earlier versions only failed "downloading" torrents at
// exactly 0% progress, so a partially-downloaded torrent that later stalled was
// treated as healthy forever.)
//
// A trickle is the case the inactivity window cannot see: a few KB/s keeps
// the last-activity timestamp fresh while the download would take a year.
// max_eta catches it, and is built so that one slow moment never does. It
// takes the BEST of three rates -- over the torrent's lifetime, over the last
// slow_window (from a sample this plugin records on every run), and the
// client's current one -- so a torrent that was fast once, sped up recently,
// or is fast right now is kept. And it judges nothing it has not watched
// downloading for a whole slow_window: a torrent that was paused, queued or
// seeding in between starts the watch again.
//
// Config keys:
//
//	stall_timeout - how long a stalled/zero-progress torrent may sit without
//	                activity before it counts as failed (default: "4h")
//	max_eta       - fail a torrent whose best rate still leaves more than this
//	                to download (default: off)
//	slow_window   - how long a torrent is watched before max_eta may fail it,
//	                and the span its recent rate is measured over (default:
//	                "24h")
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

const defaultSlowWindow = 24 * time.Hour

// RateBucketName holds each watched torrent's download samples, keyed by
// info-hash.
const RateBucketName = "torrent_failed_rates"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "accept dead torrents (errored, or stalled longer than stall_timeout); reject healthy ones",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalPerRelease,
		Requires:    plugin.RequireAll(entry.FieldTorrentState),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "stall_timeout", Type: plugin.FieldTypeDuration, Default: "4h", Hint: "Inactivity window before a stalled/zero-progress torrent counts as failed"},
			{Key: "max_eta", Type: plugin.FieldTypeDuration, Hint: "Fail a download that, at the best of its lifetime, recent and current rates, would still take longer than this to finish. Off when unset"},
			{Key: "slow_window", Type: plugin.FieldTypeDuration, Default: "24h", Hint: "max_eta: how long a download is watched before it may be failed for being slow, and the span its recent rate is measured over"},
		},
		Caches: []plugin.CacheInfo{
			{Name: RateBucketName, Display: "Janitor Download Samples"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	for _, k := range []string{"stall_timeout", "max_eta", "slow_window"} {
		if err := plugin.OptDuration(cfg, k, pluginName); err != nil {
			errs = append(errs, err)
		}
	}
	if _, ok := cfg["slow_window"]; ok {
		if _, set := cfg["max_eta"]; !set {
			errs = append(errs, fmt.Errorf("%s: 'slow_window' only applies with 'max_eta'", pluginName))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, "stall_timeout", "max_eta", "slow_window")...)
	return errs
}

type failedPlugin struct {
	stallTimeout time.Duration
	maxETA       time.Duration // 0 disables the slow rule
	slowWindow   time.Duration
	// rates is nil without a store, which disables the slow rule: it never
	// fails a torrent it has not watched.
	rates store.Bucket
	// now is the reference time; overridable in tests.
	now func() time.Time
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	p := &failedPlugin{stallTimeout: defaultStallTimeout, slowWindow: defaultSlowWindow, now: time.Now}
	for key, dst := range map[string]*time.Duration{
		"stall_timeout": &p.stallTimeout,
		"max_eta":       &p.maxETA,
		"slow_window":   &p.slowWindow,
	} {
		if v, _ := cfg[key].(string); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("%s: invalid %s %q: %w", pluginName, key, v, err)
			}
			*dst = d
		}
	}
	if db != nil {
		p.rates = db.Bucket(RateBucketName)
	}
	return p, nil
}

// sample is one observation of a torrent's all-time downloaded counter.
type sample struct {
	At         time.Time `json:"at"`
	Downloaded int64     `json:"downloaded"`
}

func (p *failedPlugin) Name() string { return pluginName }

func (p *failedPlugin) Process(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	watched := map[string]bool{}
	for _, e := range entries {
		if p.classify(tc, e) {
			watched[e.GetString(entry.FieldTorrentInfoHash)] = true
		}
	}
	p.forgetUnwatched(tc, watched)
	return entries, nil
}

// forgetUnwatched drops the samples of every torrent this run did not watch:
// one that left the session, finished, or was paused or queued. Its watch
// starts over if it comes back, so a gap is never measured as a slow spell.
func (p *failedPlugin) forgetUnwatched(tc *plugin.TaskContext, watched map[string]bool) {
	if p.rates == nil || p.maxETA <= 0 {
		return
	}
	keys, err := p.rates.Keys()
	if err != nil {
		tc.Logger.Warn(pluginName+": reading download samples", "err", err)
		return
	}
	for _, k := range keys {
		if !watched[k] {
			_ = p.rates.Delete(k)
		}
	}
}

// classify decides one torrent, and reports whether the slow rule is
// watching it.
func (p *failedPlugin) classify(tc *plugin.TaskContext, e *entry.Entry) bool {
	state := e.GetString(entry.FieldTorrentState)

	// A fully-downloaded torrent is never failed — whatever the client now
	// says about it. Its data was delivered, so the janitor's two sinks would
	// both do damage: torrent_control(remove_with_data) deletes a finished
	// download, and mark_failed un-tracks a film that is already in the
	// library, which re-downloads it on the next run.
	//
	// This check deliberately precedes the errored branch. A complete torrent
	// errors for reasons that say nothing about the download: the moved files
	// are no longer where the client expects them, the tracker went away, or
	// seeding hit an I/O error. Treating those as failed grabs was the cause
	// of repeated same-quality re-downloads.
	if getFloat(e, entry.FieldTorrentProgress) >= 100 {
		e.Reject(fmt.Sprintf("%s: healthy (complete)", pluginName))
		return false
	}

	if state == string(torrentclient.StateErrored) {
		msg := e.GetString(entry.FieldTorrentError)
		if msg == "" {
			msg = "client reported an error"
		}
		e.Accept(fmt.Sprintf("%s: errored: %s", pluginName, msg))
		tc.Logger.Info(pluginName+": failed torrent", "torrent", e.Title, "cause", "errored", "error", msg)
		return false
	}

	// A torrent counts as potentially-dead when it is stalled or downloading,
	// regardless of how far it got — the inactivity check below decides. A
	// partially-downloaded torrent that stalls is just as dead as one stuck at
	// 0%, and the client's last-activity timestamp keeps a genuinely-progressing
	// download from being flagged.
	stalled := state == string(torrentclient.StateStalled)
	downloading := state == string(torrentclient.StateDownloading)
	if !stalled && !downloading {
		e.Reject(fmt.Sprintf("%s: healthy (%s)", pluginName, state))
		return false
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
		return false
	}

	inactive := p.now().Sub(ref)
	if inactive >= p.stallTimeout {
		e.Accept(fmt.Sprintf("%s: %s with no activity for %s (stall_timeout %s)",
			pluginName, state, inactive.Round(time.Minute), p.stallTimeout))
		tc.Logger.Info(pluginName+": failed torrent", "torrent", e.Title, "cause", state, "inactive", inactive)
		return false
	}

	watched, slow := p.tooSlow(tc, e)
	if slow != "" {
		e.Accept(fmt.Sprintf("%s: too slow: %s", pluginName, slow))
		tc.Logger.Info(pluginName+": failed torrent", "torrent", e.Title, "cause", "slow", "detail", slow)
		return watched
	}
	e.Reject(fmt.Sprintf("%s: %s for %s, below stall_timeout %s",
		pluginName, state, inactive.Round(time.Minute), p.stallTimeout))
	return watched
}

// tooSlow records this run's sample and says, when the torrent has been
// watched for a whole slow_window and even its best rate leaves more than
// max_eta to go, why it is too slow. watched reports whether its samples
// are being kept.
func (p *failedPlugin) tooSlow(tc *plugin.TaskContext, e *entry.Entry) (watched bool, why string) {
	hash := e.GetString(entry.FieldTorrentInfoHash)
	if p.maxETA <= 0 || p.rates == nil || hash == "" {
		return false, ""
	}
	now := p.now()
	downloaded := getInt64(e, entry.FieldTorrentDownloaded)

	var samples []sample
	if _, err := p.rates.Get(hash, &samples); err != nil {
		samples = nil
	}
	// The counter survives a client restart but not a re-add; a smaller
	// value than last time is a new download, so its watch starts over.
	if n := len(samples); n > 0 && downloaded < samples[n-1].Downloaded {
		samples = nil
	}
	samples = append(samples, sample{At: now, Downloaded: downloaded})
	// The baseline is the newest sample at least a window old; anything
	// older than that is no longer needed.
	base := -1
	for i, s := range samples {
		if now.Sub(s.At) >= p.slowWindow {
			base = i
		}
	}
	if base > 0 {
		samples = samples[base:]
		base = 0
	}
	if err := p.rates.Put(hash, samples); err != nil {
		tc.Logger.Warn(pluginName+": storing download sample", "torrent", e.Title, "err", err)
	}
	if base < 0 {
		return true, "" // not watched for a whole window yet
	}

	size := getInt64(e, entry.FieldTorrentFileSize)
	remaining := int64(float64(size) * (100 - getFloat(e, entry.FieldTorrentProgress)) / 100)
	if remaining <= 0 {
		return true, ""
	}
	var lifetime float64
	if added := e.GetTime(entry.FieldTorrentAddedAt); !added.IsZero() {
		if age := now.Sub(added).Seconds(); age > 0 {
			lifetime = float64(downloaded) / age
		}
	}
	recent := float64(downloaded-samples[base].Downloaded) / now.Sub(samples[base].At).Seconds()
	current := float64(getInt64(e, entry.FieldTorrentDownRate))
	best := max(lifetime, recent, current)

	if best > 0 && time.Duration(float64(remaining)/best*float64(time.Second)) <= p.maxETA {
		return true, ""
	}
	left := "never, at no measurable rate"
	if best > 0 {
		left = "in about " + roughDuration(time.Duration(float64(remaining)/best*float64(time.Second)))
	}
	return true, fmt.Sprintf("%s left at best %s/s (lifetime %s/s, last %s %s/s, now %s/s): it would finish %s, past max_eta %s",
		humanBytes(float64(remaining)), humanBytes(best), humanBytes(lifetime), roughDuration(p.slowWindow),
		humanBytes(recent), humanBytes(current), left, roughDuration(p.maxETA))
}

// roughDuration is a duration a person reads: days once it is days.
func roughDuration(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return d.Round(time.Minute).String()
}

func humanBytes(b float64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%.0f B", b)
	}
	div, exp := float64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", b/div, "KMGTP"[exp])
}

// getInt64 reads a numeric field as int64 (0 when absent or non-numeric).
func getInt64(e *entry.Entry, key string) int64 {
	v, ok := e.Get(key)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
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
