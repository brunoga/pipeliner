package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/downloads"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func TestCmdDownloadedNoHistory(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.star")
	// A fresh db has no history → exit 1.
	if code := cmdDownloaded([]string{"--config", cfg, "Nonexistent Show"}); code != 1 {
		t.Errorf("expected exit 1 with no history, got %d", code)
	}
}

func TestCmdDownloadedNoArgs(t *testing.T) {
	if code := cmdDownloaded(nil); code != 1 {
		t.Errorf("expected exit 1 with no args, got %d", code)
	}
}

func TestPrintDownloadedUpgradeHistory(t *testing.T) {
	dir := t.TempDir()
	db, err := store.OpenSQLite(filepath.Join(dir, "pipeliner.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	log := downloads.New(db.Bucket(downloads.BucketName))
	log.Append(downloads.Event{MediaType: "series", Name: "star trek strange new worlds",
		DisplayName: "Star Trek Strange New Worlds", EpisodeID: "S04E05",
		Quality: quality.Parse("720p web"), DownloadedAt: base, Task: "tv"})
	log.Append(downloads.Event{MediaType: "series", Name: "star trek strange new worlds",
		DisplayName: "Star Trek Strange New Worlds", EpisodeID: "S04E05",
		Quality: quality.Parse("1080p web"), DownloadedAt: base.Add(time.Hour), Task: "tv"})
	// Mark it currently tracked.
	series.NewTracker(db.Bucket(series.TrackerBucketName)).Mark(series.Record{
		SeriesName: "star trek strange new worlds", EpisodeID: "S04E05", DownloadedAt: base.Add(time.Hour)})

	events, _ := log.Query("strange new worlds")
	hist := downloads.GroupByItem(events)
	var buf bytes.Buffer
	printDownloaded(&buf, "strange new worlds", hist,
		series.NewTracker(db.Bucket(series.TrackerBucketName)),
		movies.NewTracker(db.Bucket(movies.TrackerBucketName)))
	out := buf.String()

	if !strings.Contains(out, "2 times (quality upgrades)") {
		t.Errorf("expected upgrade count:\n%s", out)
	}
	if !strings.Contains(out, "S04E05") {
		t.Errorf("expected episode id:\n%s", out)
	}
	if !strings.Contains(out, "currently tracked: yes") {
		t.Errorf("expected currently tracked yes:\n%s", out)
	}
	if !strings.Contains(out, "1080p WEB-DL") || !strings.Contains(out, "720p WEB-DL") {
		t.Errorf("expected both quality rows:\n%s", out)
	}
}

func TestPrintDownloadedEmpty(t *testing.T) {
	var buf bytes.Buffer
	printDownloaded(&buf, "nothing", nil, nil, nil)
	if !strings.Contains(buf.String(), "no download history") {
		t.Errorf("expected no-history message: %s", buf.String())
	}
}
