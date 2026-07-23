// Package mark_failed provides a sink that records a dead torrent's original
// release URL in the shared failed-grab bucket (store.FailedBucketName) and
// un-tracks the associated episode/movie so a different release of the same
// content can be grabbed on a later run.
//
// Session entries only carry the torrent info-hash (their URL is
// torrent://<hash>), so this sink resolves the original release URL through
// the grab-record bucket that the transmission and qbittorrent sinks write
// at add time (grabs.BucketName). Entries whose hash has no grab record are
// failed with a clear reason — the torrent was added outside pipeliner or
// before grab recording existed, so there is no release URL to mark.
//
// What one successful mark does:
//
//  1. Puts the release URL into the seen_failed bucket. A seen filter
//     configured with retry_failed=true rejects that exact URL forever.
//  2. Forgets the episode in the series tracker (when the grab record has a
//     series key) or the movie in the movies tracker (movie key), so the
//     series/movies filters stop treating the content as downloaded and a
//     different release can pass.
//
// The reason stored with the failed URL is the entry's accept reason (as
// stamped by torrent_failed), overridable with the reason config key.
//
// Config keys:
//
//	reason - override for the recorded failure reason (default: the entry's
//	         accept reason, falling back to "grab failed")
package mark_failed

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/grabs"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "mark_failed"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "mark a dead torrent's release URL as failed (never re-grabbed) and un-track its episode/movie so an alternative release can be grabbed",
		Role:        plugin.RoleSink,
		Requires:    plugin.RequireAll(entry.FieldTorrentInfoHash),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "reason", Type: plugin.FieldTypeString, Hint: "Failure reason recorded with the URL (default: the entry's accept reason)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	return plugin.OptUnknownKeys(cfg, pluginName, "reason")
}

type markFailedSink struct {
	reason        string
	grabStore     *grabs.Store
	failedStore   *store.FailedStore
	seriesTracker *series.Tracker
	moviesTracker *imovies.Tracker
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	reason, _ := cfg["reason"].(string)
	return &markFailedSink{
		reason:        reason,
		grabStore:     grabs.NewStore(db.Bucket(grabs.BucketName)),
		failedStore:   store.NewFailedStore(db.Bucket(store.FailedBucketName)),
		seriesTracker: series.NewTracker(db.Bucket(series.TrackerBucketName)),
		moviesTracker: imovies.NewTracker(db.Bucket(imovies.TrackerBucketName)),
	}, nil
}

func (p *markFailedSink) Name() string { return pluginName }

// reasonFor picks the recorded failure reason: explicit config wins, then
// the entry's accept reason (stamped by torrent_failed), then a fallback.
func (p *markFailedSink) reasonFor(e *entry.Entry) string {
	if p.reason != "" {
		return p.reason
	}
	if e.AcceptReason != "" {
		return e.AcceptReason
	}
	return "grab failed"
}

func (p *markFailedSink) Consume(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		hash := strings.ToLower(e.GetString(entry.FieldTorrentInfoHash))
		if hash == "" {
			e.Fail(pluginName + ": entry has no torrent_info_hash")
			continue
		}

		rec, ok := p.grabStore.Get(hash)
		if !ok {
			e.Fail(fmt.Sprintf("%s: no grab record for hash %s — torrent was not added by a pipeliner torrent sink (or predates grab recording), original release URL unknown", pluginName, hash))
			continue
		}

		if tc.DryRun {
			e.Accept(fmt.Sprintf("%s: would mark failed: %s (%s)", pluginName, rec.URL, p.reasonFor(e)))
			tc.Logger.Info(pluginName+": dry-run", "url", rec.URL, "hash", hash)
			continue
		}

		if err := p.mark(tc, e, hash, rec); err != nil {
			e.Fail(fmt.Sprintf("%s: %v", pluginName, err))
		}
	}
	return nil
}

func (p *markFailedSink) mark(tc *plugin.TaskContext, e *entry.Entry, hash string, rec *grabs.Record) error {
	reason := p.reasonFor(e)
	if err := p.failedStore.MarkFailed(rec.URL, reason); err != nil {
		return fmt.Errorf("mark failed URL %s: %w", rec.URL, err)
	}

	// Un-track the content so the series/movies filters allow a different
	// release. Grab records without tracker keys (e.g. plain RSS→transmission
	// pipelines with no series/movies filter) have nothing to un-track.
	switch {
	case rec.SeriesName != "" && rec.EpisodeID != "":
		if err := p.seriesTracker.Forget(rec.SeriesName, rec.EpisodeID); err != nil {
			return fmt.Errorf("forget series %s %s: %w", rec.SeriesName, rec.EpisodeID, err)
		}
		tc.Logger.Info(pluginName+": episode un-tracked", "series", rec.SeriesName, "episode", rec.EpisodeID)
	case rec.MovieTitle != "":
		if err := p.moviesTracker.Forget(rec.MovieTitle, rec.MovieYear, rec.MovieIs3D); err != nil {
			return fmt.Errorf("forget movie %s (%d): %w", rec.MovieTitle, rec.MovieYear, err)
		}
		tc.Logger.Info(pluginName+": movie un-tracked", "movie", rec.MovieTitle, "year", rec.MovieYear)
	}

	// The grab record has served its purpose; drop it so the bucket doesn't
	// grow forever and a hash re-add gets a fresh record.
	if err := p.grabStore.Delete(hash); err != nil {
		tc.Logger.Warn(pluginName+": delete grab record", "hash", hash, "err", err)
	}

	e.Accept(fmt.Sprintf("%s: marked failed: %s (%s)", pluginName, rec.URL, reason))
	tc.Logger.Info(pluginName+": marked failed", "url", rec.URL, "hash", hash, "reason", reason)
	return nil
}
