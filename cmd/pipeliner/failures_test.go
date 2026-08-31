package main

import (
	"bytes"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/failures"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/task"
)

func TestLogRunFailures(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	flog := failures.New(db.Bucket(failures.BucketName))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// A result with no failures is a no-op.
	logRunFailures(flog, &task.Result{Failed: 0}, "tv", at, logger)
	if recs, _ := flog.Query("", 0); len(recs) != 0 {
		t.Fatalf("no-failure result should write nothing, got %d", len(recs))
	}

	// A result with a failed entry writes one record, with the node from the trace.
	failed := entry.New("Failed Show S01E01", "u1")
	failed.Fail("deluge: connection refused")
	res := &task.Result{
		Failed:  1,
		Entries: []*entry.Entry{failed},
		Traces: []executor.EntryTrace{{URL: "u1", Final: "failed", Steps: []executor.TraceStep{
			{Node: "deluge_7", State: "failed", Reason: "deluge: connection refused"},
		}}},
	}
	logRunFailures(flog, res, "tv", at, logger)
	recs, _ := flog.Query("", 0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 failure logged, got %d", len(recs))
	}
	if recs[0].Node != "deluge_7" || recs[0].Task != "tv" {
		t.Errorf("record = %+v", recs[0])
	}
}

func TestCmdFailuresNoneRecorded(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.star")
	if code := cmdFailures([]string{"--config", cfg}); code != 1 {
		t.Errorf("expected exit 1 when nothing recorded, got %d", code)
	}
}

func TestCmdFailuresQuery(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.star")
	db, err := store.OpenSQLite(dbPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	log := failures.New(db.Bucket(failures.BucketName))
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	log.Append(failures.Record{Title: "Star Trek Strange New Worlds S04E05 1080p",
		Reason: "deluge: connection refused", Node: "deluge_7", Task: "tv", FailedAt: base})
	db.Close()

	if code := cmdFailures([]string{"--config", cfg, "strange new worlds"}); code != 0 {
		t.Errorf("expected exit 0 with a match, got %d", code)
	}
}

func TestPrintFailuresOutput(t *testing.T) {
	recs := []failures.Record{
		{Title: "SNW S04E05", Reason: "deluge: connection refused", Node: "deluge_7", Task: "tv",
			FailedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)},
	}
	var buf bytes.Buffer
	printFailures(&buf, "", recs)
	out := buf.String()
	for _, want := range []string{"1 failure", "deluge: connection refused", "deluge_7", "SNW S04E05"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintFailuresEmpty(t *testing.T) {
	var buf bytes.Buffer
	printFailures(&buf, "", nil)
	if !strings.Contains(buf.String(), "no failures recorded") {
		t.Errorf("expected no-failures message: %s", buf.String())
	}
	buf.Reset()
	printFailures(&buf, "x", nil)
	if !strings.Contains(buf.String(), "no failures matching") {
		t.Errorf("expected no-match message: %s", buf.String())
	}
}
