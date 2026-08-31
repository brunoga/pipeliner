package stuck_favorites

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/traces"
)

func testCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "stuck", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func setup(t *testing.T) (*store.SQLiteStore, *traces.Store) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, traces.NewStore(db.Bucket(traces.BucketName))
}

func TestGenerateEmitsStuckFavorites(t *testing.T) {
	db, ts := setup(t)
	cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("cache_series_list")).
		Set("f", []match.TitleEntry{
			match.NewTitleEntry("Silo", 0),
			match.NewTitleEntry("Star Trek: Strange New Worlds", 0),
		})
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		ts.Put(traces.RunTrace{RunID: "r" + string(rune('1'+i)), Task: "tv", At: base.Add(time.Duration(i) * time.Hour),
			Entries: []executor.EntryTrace{
				{Title: "Star Trek Strange New Worlds S04E05 1080p WEB", Final: "rejected", Reason: "series: show not in list"},
			}})
	}

	p, err := newPlugin(map[string]any{}, db)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.(*stuckPlugin).Generate(context.Background(), testCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 stuck entry, got %d", len(out))
	}
	e := out[0]
	if got := e.Fields["stuck_favorite"]; got != "star trek strange new worlds" {
		t.Errorf("stuck_favorite = %v", got)
	}
	if got := e.Fields["stuck_runs"]; got != 3 {
		t.Errorf("stuck_runs = %v", got)
	}
	if e.Fields["stuck_last_reason"] != "series: show not in list" {
		t.Errorf("stuck_last_reason = %v", e.Fields["stuck_last_reason"])
	}
	if e.URL != "pipeliner://stuck/star trek strange new worlds" {
		t.Errorf("url = %q", e.URL)
	}
}

func TestGenerateRespectsMinRuns(t *testing.T) {
	db, ts := setup(t)
	cache.NewPersistent[[]match.TitleEntry](time.Hour, db.Bucket("cache_series_list")).
		Set("f", []match.TitleEntry{match.NewTitleEntry("Silo", 0)})
	base := time.Now()
	for i := 0; i < 2; i++ {
		ts.Put(traces.RunTrace{RunID: "r" + string(rune('1'+i)), Task: "tv", At: base,
			Entries: []executor.EntryTrace{{Title: "Silo S01E01 480p", Final: "rejected", Reason: "quality"}}})
	}
	// Default min_runs 3 → nothing; min_runs 2 → one.
	p, _ := newPlugin(map[string]any{}, db)
	if out, _ := p.(*stuckPlugin).Generate(context.Background(), testCtx()); len(out) != 0 {
		t.Errorf("default min_runs should yield 0, got %d", len(out))
	}
	p2, _ := newPlugin(map[string]any{"min_runs": int64(2)}, db)
	if out, _ := p2.(*stuckPlugin).Generate(context.Background(), testCtx()); len(out) != 1 {
		t.Errorf("min_runs=2 should yield 1, got %d", len(out))
	}
}

func TestGenerateNoFavorites(t *testing.T) {
	db, ts := setup(t)
	ts.Put(traces.RunTrace{RunID: "r1", Task: "tv", At: time.Now(),
		Entries: []executor.EntryTrace{{Title: "Whatever S01E01", Final: "rejected", Reason: "x"}}})
	p, _ := newPlugin(map[string]any{}, db)
	out, err := p.(*stuckPlugin).Generate(context.Background(), testCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("no favorites → no entries, got %d", len(out))
	}
}

func TestRegistered(t *testing.T) {
	if _, ok := plugin.Lookup(pluginName); !ok {
		t.Errorf("%s not registered", pluginName)
	}
}
