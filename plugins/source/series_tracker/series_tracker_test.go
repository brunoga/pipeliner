package series_tracker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func makeCtx(task string) *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   task,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func openDB(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newSource(t *testing.T, db *store.SQLiteStore) plugin.SourcePlugin {
	t.Helper()
	p, err := newPlugin(nil, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(plugin.SourcePlugin)
}

func TestGenerateFromSeededTracker(t *testing.T) {
	db := openDB(t)

	// Seed the tracker exactly the way the series filter does: through
	// series.NewTracker on the shared (non-task-namespaced) bucket.
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	dl := time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC)
	seed := []series.Record{
		{SeriesName: "breaking bad", DisplayName: "Breaking Bad", EpisodeID: "S05E16", DownloadedAt: dl},
		{SeriesName: "breaking bad", DisplayName: "Breaking Bad", EpisodeID: "S05E15", DownloadedAt: dl.Add(-24 * time.Hour)},
		{SeriesName: "severance", EpisodeID: "S02E01", DownloadedAt: dl.Add(time.Hour)},
	}
	for _, r := range seed {
		if err := tr.Mark(r); err != nil {
			t.Fatalf("Mark: %v", err)
		}
	}

	src := newSource(t, db)
	entries, err := src.Generate(context.Background(), makeCtx("some-task"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	bb := entries[0]
	if bb.Title != "Breaking Bad" {
		t.Errorf("title: got %q, want display name Breaking Bad", bb.Title)
	}
	if bb.URL != "pipeliner://series/breaking%20bad" {
		t.Errorf("url: got %q", bb.URL)
	}
	if got := bb.GetString(entry.FieldSeriesName); got != "breaking bad" {
		t.Errorf("series_name: got %q", got)
	}
	if got := bb.GetInt(entry.FieldSeriesEpisodeCount); got != 2 {
		t.Errorf("series_episode_count: got %d, want 2", got)
	}
	if got := bb.GetString(entry.FieldSeriesNewestEpisode); got != "S05E16" {
		t.Errorf("series_newest_episode: got %q, want S05E16", got)
	}
	if got := bb.GetTime(entry.FieldSeriesLastDownloadedAt); !got.Equal(dl) {
		t.Errorf("series_last_downloaded_at: got %v, want %v", got, dl)
	}
	if bb.GetBool(entry.FieldSeriesInactive) {
		t.Error("breaking bad should not be inactive")
	}
	if got := bb.GetString(entry.FieldMediaType); got != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q", got)
	}
	if got := bb.GetString(entry.FieldSource); got != "series_tracker:tracker" {
		t.Errorf("source: got %q", got)
	}

	// No DisplayName on record → title falls back to the normalized name.
	sev := entries[1]
	if sev.Title != "severance" {
		t.Errorf("fallback title: got %q, want severance", sev.Title)
	}
}

func TestGenerateSurfacesInactiveFlag(t *testing.T) {
	db := openDB(t)
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if err := tr.Mark(series.Record{SeriesName: "done show", EpisodeID: "S01E01", DownloadedAt: time.Now()}); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	inactive := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if err := inactive.Deactivate("done show", "complete"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	src := newSource(t, db)
	entries, err := src.Generate(context.Background(), makeCtx("t"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if !entries[0].GetBool(entry.FieldSeriesInactive) {
		t.Error("series_inactive should be true for a deactivated show")
	}
}

// TestCrossTaskVisibility documents the bucket-addressing contract: the
// tracker bucket is global (not namespaced by task), so records written by
// a series filter running in one task are visible to a series_tracker
// source running in a different task on the same store, with no extra
// configuration.
func TestCrossTaskVisibility(t *testing.T) {
	db := openDB(t)

	// Writer side: series filter in task "tv-shows".
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if err := tr.Mark(series.Record{SeriesName: "the wire", EpisodeID: "S05E10", DownloadedAt: time.Now()}); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	// Reader side: series_tracker source in an unrelated task.
	src := newSource(t, db)
	entries, err := src.Generate(context.Background(), makeCtx("series-lifecycle"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) != 1 || entries[0].GetString(entry.FieldSeriesName) != "the wire" {
		t.Fatalf("cross-task read failed: %+v", entries)
	}
}

func TestGenerateEmptyTracker(t *testing.T) {
	db := openDB(t)
	src := newSource(t, db)
	entries, err := src.Generate(context.Background(), makeCtx("t"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}

func TestValidateRejectsUnknownKeys(t *testing.T) {
	if errs := validate(map[string]any{"task": "x"}); len(errs) == 0 {
		t.Error("unknown key should produce a validation error")
	}
	if errs := validate(map[string]any{}); len(errs) != 0 {
		t.Errorf("empty config should validate, got %v", errs)
	}
}
