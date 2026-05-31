package dedup

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
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
	for _, e := range entries {
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		e.Set(entry.FieldSeriesEpisodeID, "S04E05")
	}

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
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
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

// TestDedupMoviesByMediaTypeAndTitle exercises the post-deprecation path:
// dedup must group movies by media_type + title rather than the deprecated
// movie_title field. Two copies of the same movie at different qualities
// should collapse to one accepted entry.
func TestDedupMoviesByMediaTypeAndTitle(t *testing.T) {
	p := &dedupPlugin{}
	low := accepted("Superman.2025.1080p.WEB-DL")
	high := accepted("Superman.2025.2160p.UHD.BluRay")
	for _, e := range []*entry.Entry{low, high} {
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
		e.Set(entry.FieldTitle, "Superman")
	}

	out, err := p.Process(context.Background(), tc(), []*entry.Entry{low, high})
	if err != nil {
		t.Fatal(err)
	}

	var winners []*entry.Entry
	for _, e := range out {
		if e.IsAccepted() {
			winners = append(winners, e)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("want 1 winner, got %d", len(winners))
	}
	if winners[0] != high {
		t.Errorf("want 2160p winner, got %q", winners[0].Title)
	}
}

// TestDedupSkipsSeriesWithoutMediaType pins the parallel behavior for series:
// an entry with series_episode_id but no media_type is no longer treated as
// a series for dedup purposes. Pipelines that want episode dedup must run
// metainfo_file (or another classifier) upstream.
func TestDedupSkipsSeriesWithoutMediaType(t *testing.T) {
	p := &dedupPlugin{}
	a := accepted("Show S01E01 720p WEB-DL")
	b := accepted("Show S01E01 1080p WEB-DL")
	for _, e := range []*entry.Entry{a, b} {
		// Only series_episode_id set; no media_type.
		e.Set(entry.FieldSeriesEpisodeID, "S01E01")
	}

	out, _ := p.Process(context.Background(), tc(), []*entry.Entry{a, b})
	var winners []*entry.Entry
	for _, e := range out {
		if e.IsAccepted() {
			winners = append(winners, e)
		}
	}
	if len(winners) != 2 {
		t.Errorf("entries without media_type must pass through unduplicated, got %d", len(winners))
	}
}

// TestDedupSkipsMovieWithoutMediaType verifies that an entry carrying only the
// legacy movie_title (no media_type) is NOT deduped by movie title under the
// new logic. This documents the intentional behavior change — pipelines that
// want movie dedup must run metainfo_file or metainfo_tmdb upstream.
func TestDedupSkipsMovieWithoutMediaType(t *testing.T) {
	p := &dedupPlugin{}
	a := accepted("Superman.2025.1080p.WEB-DL")
	b := accepted("Superman.2025.2160p.UHD.BluRay")
	for _, e := range []*entry.Entry{a, b} {
		// Only movie_title set; no media_type or title field.
		e.Set(entry.FieldMovieTitle, "Superman")
	}

	out, _ := p.Process(context.Background(), tc(), []*entry.Entry{a, b})
	var winners []*entry.Entry
	for _, e := range out {
		if e.IsAccepted() {
			winners = append(winners, e)
		}
	}
	if len(winners) != 2 {
		t.Errorf("entries without media_type must pass through unduplicated, got %d", len(winners))
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

// TestDedupRequiresErrorsWhenMediaTypeUnreachable verifies that placing dedup
// in a pipeline with no upstream producing media_type yields a validator
// error. This is the "you probably forgot metainfo_file/tmdb upstream" signal.
func TestDedupRequiresErrorsWhenMediaTypeUnreachable(t *testing.T) {
	desc, ok := plugin.Lookup("dedup")
	if !ok {
		t.Fatal("dedup plugin not registered")
	}
	src := &plugin.Descriptor{
		PluginName: "src", Role: plugin.RoleSource,
		// title is reachable, but media_type is not.
		Produces: []string{entry.FieldTitle, entry.FieldSource},
	}
	g := dag.New()
	must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }
	must(g.AddNode(&dag.Node{ID: "a", PluginName: "src"}))
	must(g.AddNode(&dag.Node{ID: "b", PluginName: "dedup", Upstreams: []dag.NodeID{"a"}}))

	reg := func(name string) (*plugin.Descriptor, bool) {
		switch name {
		case "src":
			return src, true
		case "dedup":
			return desc, true
		}
		return nil, false
	}
	errs, _ := dag.Validate(g, reg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "media_type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error mentioning media_type, got: %v", errs)
	}
}

// TestDedupRequiresWarnsWhenMediaTypeOnlyReachable verifies that when an
// upstream MayProduces media_type (as metainfo_file does), dedup gets a
// warning rather than an error — accurate: dedup will silently skip entries
// that weren't classified.
func TestDedupRequiresWarnsWhenMediaTypeOnlyReachable(t *testing.T) {
	desc, ok := plugin.Lookup("dedup")
	if !ok {
		t.Fatal("dedup plugin not registered")
	}
	src := &plugin.Descriptor{
		PluginName: "src", Role: plugin.RoleSource,
		Produces:   []string{entry.FieldTitle, entry.FieldSource},
		MayProduce: []string{entry.FieldMediaType, entry.FieldSeriesEpisodeID},
	}
	g := dag.New()
	must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }
	must(g.AddNode(&dag.Node{ID: "a", PluginName: "src"}))
	must(g.AddNode(&dag.Node{ID: "b", PluginName: "dedup", Upstreams: []dag.NodeID{"a"}}))

	reg := func(name string) (*plugin.Descriptor, bool) {
		switch name {
		case "src":
			return src, true
		case "dedup":
			return desc, true
		}
		return nil, false
	}
	errs, warnings := dag.Validate(g, reg)
	if len(errs) > 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Error(), "media_type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning mentioning media_type, got: %v", warnings)
	}
}
