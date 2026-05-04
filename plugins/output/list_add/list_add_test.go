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

func TestListAddMissingList(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{}, db)
	if err == nil {
		t.Error("expected error for missing 'list'")
	}
}

func TestListAddOutput(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()

	p := &listAddPlugin{db: db, listName: "mylist"}

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

	l := entrylist.Open(db, "mylist")
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
