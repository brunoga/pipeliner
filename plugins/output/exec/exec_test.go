package exec

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

func TestRunsCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")

	p, err := newPlugin(map[string]any{"command": "touch " + marker}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("Test", "http://x.com/a")
	if err := p.(*execPlugin).deliver(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file not created: %v", err)
	}
}

func TestTemplateInterpolation(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")

	p, _ := newPlugin(map[string]any{"command": "touch " + marker + "_{{.series_season}}"}, nil)
	e := entry.New("Test", "http://x.com/a")
	e.Set("series_season", 3)
	p.(*execPlugin).deliver(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	if _, err := os.Stat(marker + "_3"); err != nil {
		t.Errorf("expected %s_3: %v", marker, err)
	}
}

func TestFailedCommandLogged(t *testing.T) {
	p, _ := newPlugin(map[string]any{"command": "false"}, nil)
	e := entry.New("Test", "http://x.com/a")
	// Output should not propagate individual command errors.
	err := p.(*execPlugin).deliver(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Errorf("Output should not return error on per-entry command failure: %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	p, _ := newPlugin(map[string]any{"command": "sleep 60"}, nil)
	e := entry.New("Test", "http://x.com/a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Should return quickly due to cancelled context.
	p.(*execPlugin).deliver(ctx, makeCtx(), []*entry.Entry{e}) //nolint:errcheck
}

func TestMultipleEntries(t *testing.T) {
	dir := t.TempDir()
	p, _ := newPlugin(map[string]any{"command": "touch " + dir + "/{{.series_episode}}"}, nil)
	entries := []*entry.Entry{
		func() *entry.Entry { e := entry.New("A", "http://x.com/a"); e.Set("series_episode", 1); return e }(),
		func() *entry.Entry { e := entry.New("B", "http://x.com/b"); e.Set("series_episode", 2); return e }(),
	}
	p.(*execPlugin).deliver(context.Background(), makeCtx(), entries) //nolint:errcheck

	for _, ep := range []string{"1", "2"} {
		if _, err := os.Stat(filepath.Join(dir, ep)); err != nil {
			t.Errorf("marker %s not created", ep)
		}
	}
}

func TestMissingCommand(t *testing.T) {
	if _, err := newPlugin(map[string]any{}, nil); err == nil {
		t.Error("expected error when command missing")
	}
}

func TestInvalidTemplate(t *testing.T) {
	if _, err := newPlugin(map[string]any{"command": "echo {{.Unclosed"}, nil); err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("exec")
	if !ok {
		t.Fatal("exec plugin not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("phase: got %v", d.Role)
	}
}
