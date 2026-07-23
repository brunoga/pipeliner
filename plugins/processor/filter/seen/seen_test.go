package seen

import (
	"context"
	"maps"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func makeCtx(name string) *plugin.TaskContext {
	return &plugin.TaskContext{Name: name}
}

func openPlugin(t *testing.T, extra map[string]any) *seenPlugin {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := map[string]any{}
	maps.Copy(cfg, extra)
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*seenPlugin)
}

func TestNewEntry(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("Test", "http://example.com/a")
	if err := p.filter(context.Background(), makeCtx("task"), e); err != nil {
		t.Fatal(err)
	}
	if e.IsRejected() {
		t.Error("new entry should not be rejected")
	}
}

func TestSeenEntryRejected(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx("task")
	e := entry.New("Test", "http://example.com/a")
	e.Accept()

	// Mark it seen via Learn.
	if err := p.persist(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// On next run the same URL should be rejected.
	e2 := entry.New("Test", "http://example.com/a")
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsRejected() {
		t.Error("duplicate entry should be rejected")
	}
}

func TestDifferentURLsNotRejected(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx("task")

	e1 := entry.New("A", "http://example.com/a")
	e1.Accept()
	p.persist(context.Background(), tc, []*entry.Entry{e1})

	e2 := entry.New("B", "http://example.com/b")
	p.filter(context.Background(), tc, e2)
	if e2.IsRejected() {
		t.Error("different URL should not be rejected")
	}
}

func TestLocalIsolatedByTask(t *testing.T) {
	p := openPlugin(t, map[string]any{"local": true})

	e := entry.New("Test", "http://example.com/a")
	e.Accept()
	p.persist(context.Background(), makeCtx("task-1"), []*entry.Entry{e})

	// Same URL in a different task should NOT be seen.
	e2 := entry.New("Test", "http://example.com/a")
	p.filter(context.Background(), makeCtx("task-2"), e2)
	if e2.IsRejected() {
		t.Error("local seen store should not cross task boundaries")
	}

	// Same task should still reject it.
	e3 := entry.New("Test", "http://example.com/a")
	p.filter(context.Background(), makeCtx("task-1"), e3)
	if !e3.IsRejected() {
		t.Error("local seen store should reject within same task")
	}
}

func TestCustomFields(t *testing.T) {
	p := openPlugin(t, map[string]any{"fields": []any{"title"}})
	tc := makeCtx("task")

	e := entry.New("My Show S01E01", "http://x.com/a")
	e.Accept()
	p.persist(context.Background(), tc, []*entry.Entry{e})

	// Same title, different URL → should be seen.
	e2 := entry.New("My Show S01E01", "http://x.com/b")
	p.filter(context.Background(), tc, e2)
	if !e2.IsRejected() {
		t.Error("same title with different URL should be rejected when fields=[title]")
	}

	// Different title → should pass.
	e3 := entry.New("My Show S01E02", "http://x.com/a")
	p.filter(context.Background(), tc, e3)
	if e3.IsRejected() {
		t.Error("different title should not be rejected")
	}
}

func TestLearnMarksEntryAsSeen(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx("task")

	// The engine pre-filters to accepted before calling Learn; simulate that.
	accepted := entry.New("Test", "http://example.com/url")
	accepted.Accept()
	p.persist(context.Background(), tc, []*entry.Entry{accepted})

	// The accepted entry's URL should now be marked seen.
	e := entry.New("Test", "http://example.com/url")
	p.filter(context.Background(), tc, e)
	if !e.IsRejected() {
		t.Error("entry should be rejected after being marked seen via Learn")
	}
}

// TestProcess_DoesNotPersist verifies that Process() does NOT write to the seen
// store. Only Commit() should persist.
func TestProcess_DoesNotPersist(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx("task")

	e := entry.New("Test", "http://example.com/a")
	e.Accept()
	out, err := p.Process(context.Background(), tc, []*entry.Entry{e})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 output entry, got %d", len(out))
	}

	// Process must NOT have written to the seen store.
	// We verify by calling filter on the same URL — it should NOT be rejected.
	e2 := entry.New("Test", "http://example.com/a")
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if e2.IsRejected() {
		t.Error("Process() must not persist to the seen store; entry should not be seen yet")
	}
}

// TestCommit_Persists verifies that Commit() writes to the seen store.
func TestCommit_Persists(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx("task")

	e := entry.New("Test", "http://example.com/commit")
	e.Accept()

	// Process without Commit — should not be persisted.
	if _, err := p.Process(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	// Now commit.
	if err := p.Commit(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// After Commit, the URL should be seen.
	e2 := entry.New("Test", "http://example.com/commit")
	if err := p.filter(context.Background(), tc, e2); err != nil {
		t.Fatal(err)
	}
	if !e2.IsRejected() {
		t.Error("Commit() should persist to the seen store; entry should now be seen")
	}
}

func TestFingerprintStability(t *testing.T) {
	e := entry.New("My Title", "http://example.com/path")
	f1 := fingerprint(e, []string{"url", "title"})
	f2 := fingerprint(e, []string{"title", "url"})
	if f1 != f2 {
		t.Error("fingerprint should be order-independent")
	}
	if len(f1) != 64 {
		t.Errorf("fingerprint length: want 64, got %d", len(f1))
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("seen")
	if !ok {
		t.Fatal("seen plugin not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("phase: got %v", d.Role)
	}
}

func TestPluginCreation(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()
	_, err = newPlugin(map[string]any{}, db)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRetryFailedRejectsFailedURL(t *testing.T) {
	p := openPlugin(t, map[string]any{"retry_failed": true})
	tc := makeCtx("task")

	// Mark a release URL failed in the shared bucket (what mark_failed does).
	fs := store.NewFailedStore(p.db.Bucket(store.FailedBucketName))
	if err := fs.MarkFailed("http://example.com/dead.torrent", "stalled for 6h"); err != nil {
		t.Fatal(err)
	}

	dead := entry.New("Dead Release", "http://example.com/dead.torrent")
	if err := p.filter(context.Background(), tc, dead); err != nil {
		t.Fatal(err)
	}
	if !dead.IsRejected() {
		t.Fatal("failed URL should be rejected with retry_failed=true")
	}
	if dead.RejectReason != "seen: previously failed (stalled for 6h)" {
		t.Errorf("reject reason = %q", dead.RejectReason)
	}

	// A different release of the same content (different URL) passes even
	// though it was never seen — retry is possible.
	alt := entry.New("Dead Release Alt", "http://example.com/alt.torrent")
	if err := p.filter(context.Background(), tc, alt); err != nil {
		t.Fatal(err)
	}
	if alt.IsRejected() {
		t.Error("alternative release URL should not be rejected")
	}
}

func TestRetryFailedDefaultOffIgnoresFailedBucket(t *testing.T) {
	p := openPlugin(t, nil) // retry_failed defaults to false
	tc := makeCtx("task")

	fs := store.NewFailedStore(p.db.Bucket(store.FailedBucketName))
	if err := fs.MarkFailed("http://example.com/dead.torrent", "errored"); err != nil {
		t.Fatal(err)
	}

	e := entry.New("Dead Release", "http://example.com/dead.torrent")
	if err := p.filter(context.Background(), tc, e); err != nil {
		t.Fatal(err)
	}
	if e.IsRejected() {
		t.Error("failed bucket should be ignored when retry_failed is off")
	}
}
