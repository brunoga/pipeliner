// Package mark_failed provides a sink that records a dead torrent's original
// release URL in the shared failed-grab bucket (store.FailedBucketName) and
// un-tracks the associated episode/movie so a different release of the same
// content can be grabbed on a later run.
//
// Session entries only carry the torrent info-hash (their URL is
// torrent://<hash>), so this sink resolves the original release URL through
// the grab-record bucket that the deluge, transmission, and qbittorrent sinks
// write at add time (grabs.BucketName). Entries whose hash has no grab record
// are failed with a clear reason — the torrent was added outside pipeliner or
// before grab recording existed, so there is no release URL to mark.
//
// What one successful mark does:
//
//  1. Puts the release URL into the seen_failed bucket. A seen filter
//     configured with retry_failed=true rejects that exact URL forever.
//  2. Un-tracks the content so the series/movies filters stop treating it as
//     downloaded and a different release can pass. For movies this is a
//     rollback, not a delete: the movies tracker holds one record per film and
//     Mark overwrites it, so deleting would also erase an earlier successful
//     download and re-grab a film that is already in the library — usually at
//     worse quality than the copy it has. The record is restored to the
//     download it replaced, deleted only when the failed grab is the only one
//     on record, and left alone when it describes a later download than the
//     one that died.
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
	"github.com/brunoga/pipeliner/internal/untrack"
	"github.com/brunoga/pipeliner/quality"
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
	db            *store.SQLiteStore // movies trackers are opened per grab record
	untrackStore  *untrack.Store
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	reason, _ := cfg["reason"].(string)
	return &markFailedSink{
		reason:        reason,
		grabStore:     grabs.NewStore(db.Bucket(grabs.BucketName)),
		failedStore:   store.NewFailedStore(db.Bucket(store.FailedBucketName)),
		seriesTracker: series.NewTracker(db.Bucket(series.TrackerBucketName)),
		db:            db,
		untrackStore:  untrack.NewStore(db.Bucket(untrack.BucketName)),
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
	// Record under the info hash as well as the URL: indexer proxy links
	// rotate per search, so a URL-only blocklist stops matching the moment
	// the release is re-advertised.
	if err := p.failedStore.MarkFailed(hash, rec.URL, reason); err != nil {
		return fmt.Errorf("mark failed grab %s: %w", hash, err)
	}

	// Un-track the content so the series/movies filters allow a different
	// release. Grab records without tracker keys (e.g. plain RSS→transmission
	// pipelines with no series/movies filter) have nothing to un-track.
	switch {
	case rec.SeriesName != "" && rec.EpisodeID != "":
		// Roll the record back rather than deleting it, for the same reason as
		// movies below: each download overwrites the episode's record, so a
		// plain delete also discards an earlier successful download and
		// re-grabs an episode already in the library.
		outcome, err := p.seriesTracker.UntrackGrab(
			rec.SeriesName, rec.EpisodeID, rec.Quality, rec.Quality != quality.Quality{})
		if err != nil {
			return fmt.Errorf("un-track episode %s %s: %w", rec.SeriesName, rec.EpisodeID, err)
		}
		tc.Logger.Info(pluginName+": episode un-tracked", "series", rec.SeriesName,
			"episode", rec.EpisodeID, "outcome", outcome.String(),
			"failed_quality", rec.Quality.String(), "release", rec.Title)
		if outcome != untrack.Stale {
			key := untrack.EpisodeKey(rec.SeriesName, rec.EpisodeID)
			if err := p.untrackStore.Mark(key, untrack.Record{Release: rec.Title, Reason: reason}); err != nil {
				tc.Logger.Warn(pluginName+": record un-track marker", "series", rec.SeriesName, "err", err)
			}
		}
	case rec.MovieTitle != "":
		// Roll the record back rather than deleting it: the movies tracker
		// keeps one record per film, so a plain delete would also discard an
		// earlier successful download and re-grab a film already in the
		// library. hasQuality is false for grab records written before the
		// quality was captured, which only disables the staleness check.
		// The bucket comes from the grab record: a movies node with local=true
		// keeps its own tracker, and un-tracking the shared one instead would
		// both miss the record that exists and disturb another pipeline's.
		bucket := rec.MovieBucket
		if bucket == "" {
			bucket = imovies.TrackerBucketName
		}
		tracker := imovies.NewTracker(p.db.Bucket(bucket))
		outcome, err := tracker.UntrackGrab(
			rec.MovieTitle, rec.MovieYear, rec.MovieIs3D, rec.Quality, rec.Quality != quality.Quality{})
		if err != nil {
			return fmt.Errorf("un-track movie %s (%d): %w", rec.MovieTitle, rec.MovieYear, err)
		}
		tc.Logger.Info(pluginName+": movie un-tracked", "movie", rec.MovieTitle,
			"year", rec.MovieYear, "is_3d", rec.MovieIs3D, "tracker", bucket, "outcome", outcome.String(),
			"failed_quality", rec.Quality.String(), "release", rec.Title)
		// Start the movies filter's retry cooldown, so the next scheduled run
		// does not immediately grab another release of a film whose grabs keep
		// dying. Skipped when the record was left alone: it describes a later,
		// healthy download, and the normal upgrade check already governs it.
		if outcome != untrack.Stale {
			key := untrack.MovieKey(bucket, rec.MovieTitle, rec.MovieYear, rec.MovieIs3D)
			if err := p.untrackStore.Mark(key, untrack.Record{Release: rec.Title, Reason: reason}); err != nil {
				tc.Logger.Warn(pluginName+": record un-track marker", "movie", rec.MovieTitle, "err", err)
			}
		}
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
