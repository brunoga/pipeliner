package run_report

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/traces"
)

var now = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

func seed(t *testing.T, db *store.SQLiteStore) {
	t.Helper()
	ts := traces.NewStore(db.Bucket(traces.BucketName))
	put := func(rt traces.RunTrace) {
		if err := ts.Put(rt); err != nil {
			t.Fatal(err)
		}
	}
	put(traces.RunTrace{RunID: "old", Task: "tv", At: now.Add(-10 * 24 * time.Hour),
		Entries: []executor.EntryTrace{{Final: "accepted"}}})
	put(traces.RunTrace{RunID: "r1", Task: "tv", At: now.Add(-2 * time.Hour),
		Entries: []executor.EntryTrace{
			{Final: "accepted"},
			{Final: "rejected", Reason: "seen: duplicate"},
			{Final: "rejected", Reason: "seen: duplicate"},
			{Final: "rejected", Reason: "quality: too low"},
			{Final: "failed"},
		}})
	put(traces.RunTrace{RunID: "d1", Task: "tv", At: now.Add(-1 * time.Hour), DryRun: true,
		Entries: []executor.EntryTrace{{Final: "accepted"}}})
	put(traces.RunTrace{RunID: "m1", Task: "movies", At: now.Add(-3 * time.Hour),
		Entries: []executor.EntryTrace{{Final: "consumed"}}})
}

func newTestPlugin(t *testing.T, cfg map[string]any) (*reportPlugin, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if cfg == nil {
		cfg = map[string]any{}
	}
	pl, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	p := pl.(*reportPlugin)
	p.now = func() time.Time { return now }
	return p, db
}

func gen(t *testing.T, p *reportPlugin) []string {
	t.Helper()
	out, err := p.Generate(context.Background(), &plugin.TaskContext{Name: "rep", Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	titles := make([]string, len(out))
	for i, e := range out {
		titles[i] = e.Title
	}
	return titles
}

func TestReportWindowDryAndSummary(t *testing.T) {
	p, db := newTestPlugin(t, map[string]any{"window": "168h"})
	seed(t, db)

	out, err := p.Generate(context.Background(), &plugin.TaskContext{Name: "rep", Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	// old (outside window) and d1 (dry) excluded → tv/r1 + movies/m1.
	if len(out) != 2 {
		t.Fatalf("want 2 runs, got %d: %v", len(out), gen(t, p))
	}
	var tv *struct {
		title string
		f     map[string]any
	}
	for _, e := range out {
		if e.Fields["report_task"] == "tv" {
			tv = &struct {
				title string
				f     map[string]any
			}{e.Title, e.Fields}
		}
	}
	if tv == nil {
		t.Fatal("tv run missing")
	}
	if tv.title != "tv: 1 accepted, 3 rejected, 1 failed" {
		t.Errorf("title: %q", tv.title)
	}
	if tv.f["report_top_rejects"] != "seen: duplicate (2); quality: too low (1)" {
		t.Errorf("top rejects: %v", tv.f["report_top_rejects"])
	}
}

func TestReportIncludeDryAndTaskFilter(t *testing.T) {
	p, db := newTestPlugin(t, map[string]any{"include_dry": true, "tasks": []any{"tv"}})
	seed(t, db)
	titles := gen(t, p)
	// tv only, dry included, old excluded → r1 + d1.
	if len(titles) != 2 {
		t.Fatalf("want 2, got %v", titles)
	}
	foundDry := false
	for _, ti := range titles {
		if ti == "tv (dry): 1 accepted, 0 rejected, 0 failed" {
			foundDry = true
		}
	}
	if !foundDry {
		t.Errorf("dry run missing: %v", titles)
	}
}

func TestReportValidate(t *testing.T) {
	if errs := validate(map[string]any{"window": "nope"}); len(errs) == 0 {
		t.Error("bad window must fail")
	}
	if errs := validate(map[string]any{"window": "24h", "tasks": []any{"tv"}}); len(errs) != 0 {
		t.Errorf("valid: %v", errs)
	}
}
