package main

import (
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func tmpConfig(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.star")
}

func openTrackerDB(t *testing.T, cfg string) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(dbPath(cfg))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCmdTrackerMarkAndForgetSeries(t *testing.T) {
	cfg := tmpConfig(t)

	if code := cmdTracker([]string{"mark-series", "--config", cfg, "Star Trek: Strange New Worlds", "s4e5", "--quality", "1080p web"}); code != 0 {
		t.Fatalf("mark-series exit %d", code)
	}
	db := openTrackerDB(t, cfg)
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if !tr.IsSeen("star trek strange new worlds", "S04E05") {
		t.Fatalf("episode not marked under normalized/canonical key")
	}
	db.Close()

	if code := cmdTracker([]string{"forget-series", "--config", cfg, "Star Trek: Strange New Worlds", "S04E05"}); code != 0 {
		t.Fatalf("forget-series exit %d", code)
	}
	db2 := openTrackerDB(t, cfg)
	tr2 := series.NewTracker(db2.Bucket(series.TrackerBucketName))
	if tr2.IsSeen("star trek strange new worlds", "S04E05") {
		t.Errorf("episode still present after forget")
	}
}

func TestCmdTrackerMarkMovie(t *testing.T) {
	cfg := tmpConfig(t)
	if code := cmdTracker([]string{"mark-movie", "--config", cfg, "--year", "2024", "Furiosa: A Mad Max Saga"}); code != 0 {
		t.Fatalf("mark-movie exit %d", code)
	}
	db := openTrackerDB(t, cfg)
	tr := movies.NewTracker(db.Bucket(movies.TrackerBucketName))
	if !tr.IsSeen("furiosa a mad max saga", 2024, false) {
		t.Errorf("movie not marked under normalized title")
	}
}

func TestCmdTrackerBadEpisodeID(t *testing.T) {
	cfg := tmpConfig(t)
	if code := cmdTracker([]string{"mark-series", "--config", cfg, "Silo", "garbage"}); code != 1 {
		t.Errorf("expected exit 1 for bad episode id, got %d", code)
	}
}

func TestCmdTrackerUnknownOpAndNoArgs(t *testing.T) {
	if code := cmdTracker(nil); code != 1 {
		t.Errorf("expected exit 1 with no args, got %d", code)
	}
	if code := cmdTracker([]string{"frobnicate"}); code != 1 {
		t.Errorf("expected exit 1 for unknown op, got %d", code)
	}
}
