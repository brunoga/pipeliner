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
