package series_tracker_update

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func makeCtx(dryRun bool) *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DryRun: dryRun,
	}
}

func openSink(t *testing.T, cfg map[string]any) (plugin.SinkPlugin, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(plugin.SinkPlugin), db
}

func showEntry(name string) *entry.Entry {
	e := entry.New("Show", "pipeliner://series/"+name)
	e.Set(entry.FieldSeriesName, name)
	e.Accept("test")
	return e
}

func TestDeactivateWritesFlag(t *testing.T) {
	sink, db := openSink(t, map[string]any{"action": "deactivate"})
	e := showEntry("my show")
	e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleComplete)

	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	rec, ok := set.Get("my show")
	if !ok {
		t.Fatal("show should be inactive after deactivate")
	}
	// Reason defaults to the entry's series_lifecycle value.
	if rec.Reason != entry.SeriesLifecycleComplete {
		t.Errorf("reason: got %q, want complete", rec.Reason)
	}
	if !strings.Contains(e.AcceptReason, "deactivated my show") {
		t.Errorf("accept reason: got %q", e.AcceptReason)
	}
}

func TestDeactivateExplicitReasonWins(t *testing.T) {
	sink, db := openSink(t, map[string]any{"action": "deactivate", "reason": "manual"})
	e := showEntry("my show")
	e.Set(entry.FieldSeriesLifecycle, entry.SeriesLifecycleComplete)

	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	rec, _ := set.Get("my show")
	if rec == nil || rec.Reason != "manual" {
		t.Errorf("reason: got %+v, want manual", rec)
	}
}

func TestDeactivateDefaultReasonFallback(t *testing.T) {
	sink, db := openSink(t, map[string]any{"action": "deactivate"})
	e := showEntry("my show") // no series_lifecycle field

	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	rec, _ := set.Get("my show")
	if rec == nil || rec.Reason != "deactivated" {
		t.Errorf("reason: got %+v, want deactivated", rec)
	}
}

func TestReactivateRemovesFlag(t *testing.T) {
	sink, db := openSink(t, map[string]any{"action": "reactivate"})
	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if err := set.Deactivate("my show", "complete"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	e := showEntry("my show")
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if set.IsInactive("my show") {
		t.Error("show should be active after reactivate")
	}
}

func TestDryRunMakesNoWrites(t *testing.T) {
	sink, db := openSink(t, map[string]any{"action": "deactivate"})
	e := showEntry("my show")

	if err := sink.Consume(context.Background(), makeCtx(true), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if set.IsInactive("my show") {
		t.Error("dry-run must not write the inactive flag")
	}
	keys, err := db.Bucket(series.InactiveBucketName).Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("dry-run left %d keys in the bucket", len(keys))
	}
	if !strings.Contains(e.AcceptReason, "would deactivate my show") {
		t.Errorf("dry-run accept reason: got %q", e.AcceptReason)
	}
}

func TestDryRunReactivateMakesNoWrites(t *testing.T) {
	sink, db := openSink(t, map[string]any{"action": "reactivate"})
	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if err := set.Deactivate("my show", "complete"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	e := showEntry("my show")
	if err := sink.Consume(context.Background(), makeCtx(true), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !set.IsInactive("my show") {
		t.Error("dry-run reactivate must not remove the flag")
	}
	if !strings.Contains(e.AcceptReason, "would reactivate my show") {
		t.Errorf("dry-run accept reason: got %q", e.AcceptReason)
	}
}

func TestMissingSeriesNameFailsEntry(t *testing.T) {
	sink, _ := openSink(t, map[string]any{"action": "deactivate"})
	e := entry.New("No Name", "http://x/1")
	e.Accept("test")

	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !e.IsFailed() {
		t.Error("entry without series_name should be failed")
	}
}

func TestFactoryRejectsBadAction(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()
	if _, err := newPlugin(map[string]any{"action": "remove"}, db); err == nil {
		t.Error("unknown action should fail plugin construction")
	}
	if _, err := newPlugin(map[string]any{}, db); err == nil {
		t.Error("missing action should fail plugin construction")
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Error("missing action should fail validation")
	}
	if errs := validate(map[string]any{"action": "remove"}); len(errs) == 0 {
		t.Error("unknown action should fail validation")
	}
	if errs := validate(map[string]any{"action": "deactivate", "bogus": 1}); len(errs) == 0 {
		t.Error("unknown key should fail validation")
	}
	if errs := validate(map[string]any{"action": "reactivate", "reason": "oops, undo"}); len(errs) != 0 {
		t.Errorf("valid config should pass, got %v", errs)
	}
}
