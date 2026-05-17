package dedup

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func accepted(title string) *entry.Entry {
	e := entry.New(title, "http://example.com/"+title)
	e.Accept()
	return e
}

// TestDedupCaseInsensitiveSeriesName is the regression test for the "FROM" vs
// "From" bug: entries for the same show parsed with different letter casing
// must collapse to a single dedup group.
func TestDedupCaseInsensitiveSeriesName(t *testing.T) {
	p := &dedupPlugin{}
	entries := []*entry.Entry{
		accepted("FROM S04E05 What A Long Strange Trip 2160p AMZN WEB-DL H.265-Kitsune"),
		accepted("From S04E05 1080p WEB h264-GRACE"),
		accepted("FROM S04E05 720p HEVC x265-MeGusta"),
	}
	entries[0].Set(entry.FieldSeriesEpisodeID, "S04E05")
	entries[1].Set(entry.FieldSeriesEpisodeID, "S04E05")
	entries[2].Set(entry.FieldSeriesEpisodeID, "S04E05")

	out, err := p.Process(context.Background(), tc(), entries)
	if err != nil {
		t.Fatal(err)
	}

	accepted := 0
	for _, e := range out {
		if e.IsAccepted() {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("want exactly 1 accepted entry (best quality), got %d", accepted)
	}
}

func TestDedupKeepsBestResolution(t *testing.T) {
	p := &dedupPlugin{}
	entries := []*entry.Entry{
		accepted("Show S01E01 720p WEB-DL"),
		accepted("Show S01E01 1080p WEB-DL"),
		accepted("Show S01E01 480p WEB-DL"),
	}
	for _, e := range entries {
		e.Set(entry.FieldSeriesEpisodeID, "S01E01")
	}

	out, _ := p.Process(context.Background(), tc(), entries)
	var winner *entry.Entry
	for _, e := range out {
		if e.IsAccepted() {
			winner = e
		}
	}
	if winner == nil {
		t.Fatal("no accepted entry")
	}
	if winner.Title != "Show S01E01 1080p WEB-DL" {
		t.Errorf("want 1080p winner, got %q", winner.Title)
	}
}

func TestDedupPassesThroughEntriesWithNoKey(t *testing.T) {
	p := &dedupPlugin{}
	e := accepted("Some article with no media key")
	out, _ := p.Process(context.Background(), tc(), []*entry.Entry{e})
	if len(out) != 1 || !out[0].IsAccepted() {
		t.Error("entry without dedup key should pass through")
	}
}
