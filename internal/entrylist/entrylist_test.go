package entrylist

import (
	"sort"
	"testing"

	"github.com/brunoga/pipeliner/internal/store"
)

func openTestList(t *testing.T, name string) *List {
	t.Helper()
	s, err := store.OpenSQLite(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return Open(s, name)
}

func TestAdd(t *testing.T) {
	l := openTestList(t, "test")
	if err := l.Add("Show S01E01", "http://example.com/1"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	found, err := l.Contains("Show S01E01")
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if !found {
		t.Error("expected entry to be in list after Add")
	}
}

func TestContainsAbsent(t *testing.T) {
	l := openTestList(t, "test")
	found, err := l.Contains("Not Here")
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if found {
		t.Error("expected entry to be absent")
	}
}

func TestRemove(t *testing.T) {
	l := openTestList(t, "test")
	if err := l.Add("Title", "http://example.com"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := l.Remove("Title"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	found, err := l.Contains("Title")
	if err != nil {
		t.Fatalf("Contains after Remove: %v", err)
	}
	if found {
		t.Error("expected entry to be absent after Remove")
	}
}

func TestTitles(t *testing.T) {
	l := openTestList(t, "test")
	entries := []string{"Alpha", "Beta", "Gamma"}
	for _, title := range entries {
		if err := l.Add(title, "http://example.com/"+title); err != nil {
			t.Fatalf("Add %q: %v", title, err)
		}
	}
	titles, err := l.Titles()
	if err != nil {
		t.Fatalf("Titles: %v", err)
	}
	sort.Strings(titles)
	sort.Strings(entries)
	if len(titles) != len(entries) {
		t.Fatalf("expected %d titles, got %d: %v", len(entries), len(titles), titles)
	}
	for i, want := range entries {
		if titles[i] != want {
			t.Errorf("titles[%d] = %q, want %q", i, titles[i], want)
		}
	}
}
