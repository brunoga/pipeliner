package list_match

import (
	"context"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/entrylist"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func makeTC() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test-task",
		Logger: slog.Default(),
	}
}

func TestListMatchMissingList(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()
	_, err := newPlugin(map[string]any{}, db)
	if err == nil {
		t.Error("expected error for missing 'list'")
	}
}

func TestListMatchFound(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()

	l := entrylist.Open(db, "mylist")
	if err := l.Add("Show S01E01", "http://example.com/1"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	p := &listMatchPlugin{db: db, listName: "mylist"}
	e := entry.New("Show S01E01", "http://example.com/1")
	if err := p.Filter(context.Background(), makeTC(), e); err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if !e.IsAccepted() {
		t.Errorf("expected entry to be accepted, got %s", e.State)
	}
}

func TestListMatchNotFound(t *testing.T) {
	db, _ := store.OpenSQLite(":memory:")
	defer db.Close()

	p := &listMatchPlugin{db: db, listName: "mylist"}
	e := entry.New("Not In List", "http://example.com/x")
	if err := p.Filter(context.Background(), makeTC(), e); err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if !e.IsRejected() {
		t.Errorf("expected entry to be rejected, got %s", e.State)
	}
}

func TestListMatchRemoveOnMatch(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()

	l := entrylist.Open(db, "mylist")
	if err := l.Add("Episode One", "http://example.com/1"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	p := &listMatchPlugin{db: db, listName: "mylist", removeOnMatch: true}
	e := entry.New("Episode One", "http://example.com/1")
	if err := p.Filter(context.Background(), makeTC(), e); err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if !e.IsAccepted() {
		t.Errorf("expected entry to be accepted")
	}

	l2 := entrylist.Open(db, "mylist")
	found, err := l2.Contains("Episode One")
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if found {
		t.Error("expected entry to be removed from list after match")
	}
}

func TestListMatchIsRegistered(t *testing.T) {
	d, ok := plugin.Lookup("list_match")
	if !ok {
		t.Fatal("list_match plugin is not registered")
	}
	if d.PluginPhase != plugin.PhaseFilter {
		t.Errorf("expected phase %v, got %v", plugin.PhaseFilter, d.PluginPhase)
	}
}
