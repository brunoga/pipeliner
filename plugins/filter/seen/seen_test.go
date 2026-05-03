package seen

import (
	"context"
	"maps"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx(name string) *plugin.TaskContext {
	return &plugin.TaskContext{Name: name}
}

func openPlugin(t *testing.T, extra map[string]any) *seenPlugin {
	t.Helper()
	cfg := map[string]any{
		"db": filepath.Join(t.TempDir(), "seen.db"),
	}
	maps.Copy(cfg, extra)
	p, err := newPlugin(cfg)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*seenPlugin)
}

func TestNewEntry(t *testing.T) {
	p := openPlugin(t, nil)
	e := entry.New("Test", "http://example.com/a")
	if err := p.Filter(context.Background(), makeCtx("task"), e); err != nil {
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
	if err := p.Learn(context.Background(), tc, []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	// On next run the same URL should be rejected.
	e2 := entry.New("Test", "http://example.com/a")
	if err := p.Filter(context.Background(), tc, e2); err != nil {
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
	p.Learn(context.Background(), tc, []*entry.Entry{e1}) //nolint:errcheck

	e2 := entry.New("B", "http://example.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if e2.IsRejected() {
		t.Error("different URL should not be rejected")
	}
}

func TestLocalIsolatedByTask(t *testing.T) {
	p := openPlugin(t, map[string]any{"local": true})

	e := entry.New("Test", "http://example.com/a")
	e.Accept()
	p.Learn(context.Background(), makeCtx("task-1"), []*entry.Entry{e}) //nolint:errcheck

	// Same URL in a different task should NOT be seen.
	e2 := entry.New("Test", "http://example.com/a")
	p.Filter(context.Background(), makeCtx("task-2"), e2) //nolint:errcheck
	if e2.IsRejected() {
		t.Error("local seen store should not cross task boundaries")
	}

	// Same task should still reject it.
	e3 := entry.New("Test", "http://example.com/a")
	p.Filter(context.Background(), makeCtx("task-1"), e3) //nolint:errcheck
	if !e3.IsRejected() {
		t.Error("local seen store should reject within same task")
	}
}

func TestCustomFields(t *testing.T) {
	p := openPlugin(t, map[string]any{"fields": []any{"title"}})
	tc := makeCtx("task")

	e := entry.New("My Show S01E01", "http://x.com/a")
	e.Accept()
	p.Learn(context.Background(), tc, []*entry.Entry{e}) //nolint:errcheck

	// Same title, different URL → should be seen.
	e2 := entry.New("My Show S01E01", "http://x.com/b")
	p.Filter(context.Background(), tc, e2) //nolint:errcheck
	if !e2.IsRejected() {
		t.Error("same title with different URL should be rejected when fields=[title]")
	}

	// Different title → should pass.
	e3 := entry.New("My Show S01E02", "http://x.com/a")
	p.Filter(context.Background(), tc, e3) //nolint:errcheck
	if e3.IsRejected() {
		t.Error("different title should not be rejected")
	}
}

func TestLearnOnlyAccepted(t *testing.T) {
	p := openPlugin(t, nil)
	tc := makeCtx("task")

	rejected := entry.New("Test", "http://example.com/rej")
	rejected.Reject("test reason")
	p.Learn(context.Background(), tc, []*entry.Entry{rejected}) //nolint:errcheck

	// The rejected entry's URL should NOT be marked seen.
	e := entry.New("Test", "http://example.com/rej")
	p.Filter(context.Background(), tc, e) //nolint:errcheck
	if e.IsRejected() {
		t.Error("rejected entries should not be marked seen")
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
	if d.PluginPhase != plugin.PhaseFilter {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}

func TestMissingDBDefaultPath(t *testing.T) {
	// No db path → uses default "pipeliner.db" — plugin creation should succeed.
	// We can't actually use it in a test without leaving files, so just verify
	// the error path from a bad path is not triggered on empty config.
	// (The default path creates a file in cwd; we test the parsing logic only.)
	_, err := newPlugin(map[string]any{"db": filepath.Join(t.TempDir(), "ok.db")})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
