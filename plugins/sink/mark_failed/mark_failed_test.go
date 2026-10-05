package mark_failed

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/grabs"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/untrack"
	"github.com/brunoga/pipeliner/quality"
)

const hash = "abcdef0123456789abcdef0123456789abcdef01"
const releaseURL = "https://indexer.example.com/release/42.torrent"

func makeCtx(dryRun bool) *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "janitor",
		DryRun: dryRun,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func openSink(t *testing.T, cfg map[string]any) (*markFailedSink, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*markFailedSink), db
}

func sessionEntry(h string) *entry.Entry {
	e := entry.New("Some.Torrent.S01E03.720p", "torrent://"+h)
	e.Set(entry.FieldTorrentInfoHash, h)
	e.Accept("torrent_failed: errored: tracker unregistered")
	return e
}

func TestMarkFailedResolvesURLAndForgetsSeries(t *testing.T) {
	p, db := openSink(t, nil)

	// Simulate the transmission sink's add-time grab record.
	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL:        releaseURL,
		Title:      "Some.Torrent.S01E03.720p",
		SeriesName: "some torrent",
		EpisodeID:  "S01E03",
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate the series filter's commit having tracked the episode.
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if err := tr.Mark(series.Record{
		SeriesName: "some torrent", EpisodeID: "S01E03", DownloadedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	e := sessionEntry(hash)
	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	if !e.IsAccepted() {
		t.Fatalf("entry state = %v (%q)", e.State, e.FailReason)
	}
	if !strings.Contains(e.AcceptReason, releaseURL) {
		t.Errorf("accept reason = %q", e.AcceptReason)
	}

	// Release URL is in the failed bucket with the torrent_failed reason.
	fs := store.NewFailedStore(db.Bucket(store.FailedBucketName))
	rec, ok := fs.Get(releaseURL)
	if !ok {
		t.Fatal("release URL should be in the failed bucket")
	}
	if !strings.Contains(rec.Reason, "tracker unregistered") {
		t.Errorf("failed reason = %q", rec.Reason)
	}

	// Episode is no longer considered downloaded.
	if tr.IsSeen("some torrent", "S01E03") {
		t.Error("episode should be forgotten in the series tracker")
	}

	// The grab record was consumed.
	if _, ok := gs.Get(hash); ok {
		t.Error("grab record should be deleted after marking")
	}
}

func TestMarkFailedForgetsMovie(t *testing.T) {
	p, db := openSink(t, nil)

	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL:        releaseURL,
		MovieTitle: "dune part two",
		MovieYear:  2024,
	}); err != nil {
		t.Fatal(err)
	}
	mt := imovies.NewTracker(db.Bucket(imovies.TrackerBucketName))
	if err := mt.Mark(imovies.Record{Title: "dune part two", Year: 2024}); err != nil {
		t.Fatal(err)
	}

	e := sessionEntry(hash)
	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() {
		t.Fatalf("entry state = %v (%q)", e.State, e.FailReason)
	}
	if mt.IsSeen("dune part two", 2024, false) {
		t.Error("movie should be forgotten in the movies tracker")
	}
}

func TestMissingMappingFails(t *testing.T) {
	p, db := openSink(t, nil)

	e := sessionEntry(hash) // no grab record written
	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsFailed() {
		t.Fatal("entry without a grab record should be failed")
	}
	if !strings.Contains(e.FailReason, "no grab record") {
		t.Errorf("fail reason = %q", e.FailReason)
	}

	// Nothing was written to the failed bucket.
	fs := store.NewFailedStore(db.Bucket(store.FailedBucketName))
	if fs.IsFailed(releaseURL) {
		t.Error("failed bucket should be untouched")
	}
}

func TestMissingHashFails(t *testing.T) {
	p, _ := openSink(t, nil)
	e := entry.New("No.Hash", "torrent://")
	e.Accept()
	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsFailed() {
		t.Fatal("entry without torrent_info_hash should be failed")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	p, db := openSink(t, nil)

	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{URL: releaseURL, SeriesName: "x", EpisodeID: "S01E01"}); err != nil {
		t.Fatal(err)
	}

	e := sessionEntry(hash)
	if err := p.Consume(context.Background(), makeCtx(true), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsAccepted() || !strings.Contains(e.AcceptReason, "would mark failed") {
		t.Errorf("dry-run accept reason = %q (state %v)", e.AcceptReason, e.State)
	}

	fs := store.NewFailedStore(db.Bucket(store.FailedBucketName))
	if fs.IsFailed(releaseURL) {
		t.Error("dry-run must not write to the failed bucket")
	}
	if _, ok := gs.Get(hash); !ok {
		t.Error("dry-run must not delete the grab record")
	}
}

func TestReasonOverride(t *testing.T) {
	p, db := openSink(t, map[string]any{"reason": "manual purge"})

	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{URL: releaseURL}); err != nil {
		t.Fatal(err)
	}

	e := sessionEntry(hash)
	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	fs := store.NewFailedStore(db.Bucket(store.FailedBucketName))
	rec, ok := fs.Get(releaseURL)
	if !ok {
		t.Fatal("URL should be marked failed")
	}
	if rec.Reason != "manual purge" {
		t.Errorf("reason = %q, want manual purge", rec.Reason)
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{"reason": "x"}); len(errs) != 0 {
		t.Errorf("valid config produced errors: %v", errs)
	}
	if errs := validate(map[string]any{"bogus": true}); len(errs) == 0 {
		t.Error("unknown key should error")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup(pluginName)
	if !ok {
		t.Fatal("mark_failed not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("role = %v", d.Role)
	}
	if len(d.Requires) != 1 || d.Requires[0][0] != entry.FieldTorrentInfoHash {
		t.Errorf("Requires = %v", d.Requires)
	}
}

// TestFailedUpgradeRollsBackInsteadOfForgetting is the regression guard for
// the headline bug: the movies tracker holds one record per film and Mark
// overwrites it, so deleting the record on a failed grab also erased the good
// copy already in the library — and the next run re-downloaded the film, often
// at worse quality than it already had.
func TestFailedUpgradeRollsBackInsteadOfForgetting(t *testing.T) {
	p, db := openSink(t, nil)
	have := quality.Parse("2160p bluray x265 truehd dolby vision")
	attempted := quality.Parse("2160p remux x265 atmos dolby vision")

	tr := imovies.NewTracker(db.Bucket(imovies.TrackerBucketName))
	// The copy in the library, then the upgrade attempt that superseded it.
	if err := tr.Mark(imovies.Record{Title: "sinners", Year: 2025, Quality: have}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Mark(imovies.Record{Title: "sinners", Year: 2025, Quality: attempted}); err != nil {
		t.Fatal(err)
	}

	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL:        releaseURL,
		Title:      "Sinners 2025 UHD Remux",
		MovieTitle: "sinners",
		MovieYear:  2025,
		Quality:    attempted,
	}); err != nil {
		t.Fatal(err)
	}

	e := sessionEntry(hash)
	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	rec, ok := tr.Latest("sinners", false)
	if !ok {
		t.Fatal("the copy already in the library must stay tracked")
	}
	if rec.Quality != have {
		t.Errorf("tracked quality = %s, want the library copy %s", rec.Quality, have)
	}
}

// With only one download on record there is nothing to roll back to, so the
// record goes and another release can be tried.
func TestFailedFirstGrabDeletesRecord(t *testing.T) {
	p, db := openSink(t, nil)
	attempted := quality.Parse("1080p web-dl x264")

	tr := imovies.NewTracker(db.Bucket(imovies.TrackerBucketName))
	if err := tr.Mark(imovies.Record{Title: "mutiny", Year: 2026, Quality: attempted}); err != nil {
		t.Fatal(err)
	}
	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL: releaseURL, Title: "Mutiny 2026 1080p", MovieTitle: "mutiny",
		MovieYear: 2026, Quality: attempted,
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{sessionEntry(hash)}); err != nil {
		t.Fatal(err)
	}
	if tr.IsSeen("mutiny", 2026, false) {
		t.Error("record should be gone so a different release can be grabbed")
	}
}

// A torrent that dies long after a newer, healthy download replaced its record
// must not disturb that record.
func TestStaleFailureLeavesLaterRecord(t *testing.T) {
	p, db := openSink(t, nil)
	old := quality.Parse("1080p web-dl x264")
	current := quality.Parse("2160p bluray x265 atmos")

	tr := imovies.NewTracker(db.Bucket(imovies.TrackerBucketName))
	if err := tr.Mark(imovies.Record{Title: "mutiny", Year: 2026, Quality: current}); err != nil {
		t.Fatal(err)
	}
	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL: releaseURL, Title: "Mutiny 2026 1080p", MovieTitle: "mutiny",
		MovieYear: 2026, Quality: old,
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{sessionEntry(hash)}); err != nil {
		t.Fatal(err)
	}
	rec, ok := tr.Latest("mutiny", false)
	if !ok || rec.Quality != current {
		t.Errorf("the later download must stay tracked, got %+v", rec)
	}
	// No hold either: the record is healthy, so the normal upgrade check governs.
	if _, held := untrack.NewStore(db.Bucket(untrack.BucketName)).Remaining(
		untrack.MovieKey("mutiny", 2026, false), 6*time.Hour, time.Now()); held {
		t.Error("a stale failure must not start a retry cooldown")
	}
}

// Un-tracking starts the movies filter's retry hold, so the next scheduled run
// does not immediately grab another release of the same film.
func TestUntrackStartsRetryCooldown(t *testing.T) {
	p, db := openSink(t, nil)
	attempted := quality.Parse("1080p bluray x264")

	tr := imovies.NewTracker(db.Bucket(imovies.TrackerBucketName))
	if err := tr.Mark(imovies.Record{Title: "aladdin", Year: 2019, Quality: attempted}); err != nil {
		t.Fatal(err)
	}
	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL: releaseURL, Title: "Aladdin 2019 1080p BluRay x264-DON",
		MovieTitle: "aladdin", MovieYear: 2019, Quality: attempted,
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{sessionEntry(hash)}); err != nil {
		t.Fatal(err)
	}

	us := untrack.NewStore(db.Bucket(untrack.BucketName))
	left, held := us.Remaining(untrack.MovieKey("aladdin", 2019, false), 6*time.Hour, time.Now())
	if !held {
		t.Fatal("un-tracking should start the retry hold")
	}
	if left <= 0 || left > 6*time.Hour {
		t.Errorf("remaining hold = %s, want within the 6h window", left)
	}
	marker, ok := us.Last(untrack.MovieKey("aladdin", 2019, false))
	if !ok || marker.Release != "Aladdin 2019 1080p BluRay x264-DON" {
		t.Errorf("marker should name the release that died, got %+v", marker)
	}
}

// A dry run must not touch the tracker or start a hold.
func TestDryRunDoesNotUntrack(t *testing.T) {
	p, db := openSink(t, nil)
	attempted := quality.Parse("1080p bluray x264")

	tr := imovies.NewTracker(db.Bucket(imovies.TrackerBucketName))
	if err := tr.Mark(imovies.Record{Title: "aladdin", Year: 2019, Quality: attempted}); err != nil {
		t.Fatal(err)
	}
	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL: releaseURL, MovieTitle: "aladdin", MovieYear: 2019, Quality: attempted,
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.Consume(context.Background(), makeCtx(true), []*entry.Entry{sessionEntry(hash)}); err != nil {
		t.Fatal(err)
	}
	if !tr.IsSeen("aladdin", 2019, false) {
		t.Error("dry run must leave the tracker alone")
	}
	if _, held := untrack.NewStore(db.Bucket(untrack.BucketName)).Remaining(
		untrack.MovieKey("aladdin", 2019, false), 6*time.Hour, time.Now()); held {
		t.Error("dry run must not start a hold")
	}
}

// TestFailedEpisodeUpgradeRollsBack mirrors the movies case: the series
// tracker keeps one record per episode and each download overwrites it, so
// deleting on a failed grab also erased the copy already in the library and
// re-downloaded the episode.
func TestFailedEpisodeUpgradeRollsBack(t *testing.T) {
	p, db := openSink(t, nil)
	have := quality.Parse("1080p BluRay x265")
	attempted := quality.Parse("2160p WEB-DL x265")

	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if err := tr.Mark(series.Record{SeriesName: "some torrent", EpisodeID: "S01E03", Quality: have}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Mark(series.Record{SeriesName: "some torrent", EpisodeID: "S01E03", Quality: attempted}); err != nil {
		t.Fatal(err)
	}

	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL: releaseURL, Title: "Some.Torrent.S01E03.2160p",
		SeriesName: "some torrent", EpisodeID: "S01E03", Quality: attempted,
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{sessionEntry(hash)}); err != nil {
		t.Fatal(err)
	}

	rec, ok := tr.Get("some torrent", "S01E03")
	if !ok {
		t.Fatal("the copy already in the library must stay tracked")
	}
	if rec.Quality != have {
		t.Errorf("tracked quality = %s, want the library copy %s", rec.Quality, have)
	}
	// The hold is started so the next run does not immediately try again.
	if _, held := untrack.NewStore(db.Bucket(untrack.BucketName)).Remaining(
		untrack.EpisodeKey("some torrent", "S01E03"), 6*time.Hour, time.Now()); !held {
		t.Error("un-tracking an episode should start the retry hold")
	}
}

// A torrent that dies after a newer, healthy episode download replaced its
// record must not disturb that record, and must not start a hold.
func TestStaleEpisodeFailureLeavesLaterRecord(t *testing.T) {
	p, db := openSink(t, nil)
	old := quality.Parse("720p WEB-DL x264")
	current := quality.Parse("1080p BluRay x265")

	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if err := tr.Mark(series.Record{SeriesName: "some torrent", EpisodeID: "S01E03", Quality: current}); err != nil {
		t.Fatal(err)
	}
	gs := grabs.NewStore(db.Bucket(grabs.BucketName))
	if err := gs.Put(hash, grabs.Record{
		URL: releaseURL, SeriesName: "some torrent", EpisodeID: "S01E03", Quality: old,
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.Consume(context.Background(), makeCtx(false), []*entry.Entry{sessionEntry(hash)}); err != nil {
		t.Fatal(err)
	}
	rec, ok := tr.Get("some torrent", "S01E03")
	if !ok || rec.Quality != current {
		t.Errorf("the later download must stay tracked, got %+v", rec)
	}
	if _, held := untrack.NewStore(db.Bucket(untrack.BucketName)).Remaining(
		untrack.EpisodeKey("some torrent", "S01E03"), 6*time.Hour, time.Now()); held {
		t.Error("a stale failure must not start a retry cooldown")
	}
}
