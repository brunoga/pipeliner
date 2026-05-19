package executor

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

func TestPortBreakdown_Empty(t *testing.T) {
	if got := portBreakdown(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestPortBreakdown_NoPortTag(t *testing.T) {
	e := entry.New("title", "http://example.com")
	e.Accept()
	if got := portBreakdown([]*entry.Entry{e}); got != "" {
		t.Errorf("got %q, want empty for entries without port tag", got)
	}
}

func TestPortBreakdown_SinglePort(t *testing.T) {
	entries := makePortEntries(t, "series", 3)
	got := portBreakdown(entries)
	if got != "series=3" {
		t.Errorf("got %q, want %q", got, "series=3")
	}
}

func TestPortBreakdown_MultiplePorts(t *testing.T) {
	var entries []*entry.Entry
	entries = append(entries, makePortEntries(t, "series", 2)...)
	entries = append(entries, makePortEntries(t, "movies", 3)...)
	got := portBreakdown(entries)
	if got != "series=2 movies=3" {
		t.Errorf("got %q, want %q", got, "series=2 movies=3")
	}
}

func TestPortBreakdown_PreservesFirstSeenOrder(t *testing.T) {
	var entries []*entry.Entry
	entries = append(entries, makePortEntries(t, "movies", 1)...)
	entries = append(entries, makePortEntries(t, "series", 2)...)
	entries = append(entries, makePortEntries(t, "movies", 1)...)
	got := portBreakdown(entries)
	if got != "movies=2 series=2" {
		t.Errorf("got %q, want %q", got, "movies=2 series=2")
	}
}

func TestPortBreakdown_MixedTaggedAndUntagged(t *testing.T) {
	untagged := entry.New("plain", "http://example.com/plain")
	untagged.Accept()
	tagged := makePortEntries(t, "series", 1)
	got := portBreakdown(append([]*entry.Entry{untagged}, tagged...))
	if got != "series=1" {
		t.Errorf("got %q, want %q", got, "series=1")
	}
}

func makePortEntries(t *testing.T, port string, n int) []*entry.Entry {
	t.Helper()
	out := make([]*entry.Entry, n)
	for i := range out {
		e := entry.New("title", "http://example.com/"+port)
		e.Set(entry.FieldRoutePort, port)
		e.Accept()
		out[i] = e
	}
	return out
}
