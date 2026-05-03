package list_add

import (
	"context"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/entrylist"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func TestListAddMissingDB(t *testing.T) {
	_, err := newPlugin(map[string]any{"list": "mylist"})
	if err == nil {
		t.Error("expected error for missing 'db'")
	}
}

func TestListAddMissingList(t *testing.T) {
	_, err := newPlugin(map[string]any{"db": "/tmp/test.db"})
	if err == nil {
		t.Error("expected error for missing 'list'")
	}
}

func TestListAddOutput(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	p := &listAddPlugin{dbPath: dbPath, listName: "mylist"}

	tc := &plugin.TaskContext{
		Name:   "test-task",
		Logger: slog.Default(),
	}
	entries := []*entry.Entry{
		entry.New("Show S01E01", "http://example.com/1"),
		entry.New("Show S01E02", "http://example.com/2"),
	}

	if err := p.Output(context.Background(), tc, entries); err != nil {
		t.Fatalf("Output: %v", err)
	}

	// Verify entries are stored.
	s, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer s.Close()

	l := entrylist.Open(s, "mylist")
	for _, e := range entries {
		found, err := l.Contains(e.Title)
		if err != nil {
			t.Fatalf("Contains %q: %v", e.Title, err)
		}
		if !found {
			t.Errorf("expected %q to be in list", e.Title)
		}
	}
}

func TestListAddIsRegistered(t *testing.T) {
	d, ok := plugin.Lookup("list_add")
	if !ok {
		t.Fatal("list_add plugin is not registered")
	}
	if d.PluginPhase != plugin.PhaseOutput {
		t.Errorf("expected phase %v, got %v", plugin.PhaseOutput, d.PluginPhase)
	}
}
