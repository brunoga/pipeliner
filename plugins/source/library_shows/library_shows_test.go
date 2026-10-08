package library_shows

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/mediaserver"
	"github.com/brunoga/pipeliner/internal/plugin"
)

type stubServer struct {
	items []mediaserver.Item
	err   error
}

func (s *stubServer) ListItems(context.Context) ([]mediaserver.Item, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.items, nil
}
func (s *stubServer) Refresh(context.Context) error { return nil }

func episode(show string, season, ep int, section string) mediaserver.Item {
	return mediaserver.Item{
		Type: "episode", Show: show, Season: season, Episode: ep, Section: section,
	}
}

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newWithServer(srv *stubServer, sections mediaserver.Sections) *showsSourcePlugin {
	return &showsSourcePlugin{client: srv, sections: sections}
}

func run(t *testing.T, p *showsSourcePlugin) []*entry.Entry {
	t.Helper()
	out, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return out
}

func TestEmitsOneEntryPerShow(t *testing.T) {
	srv := &stubServer{items: []mediaserver.Item{
		episode("The Wire", 1, 1, "TV Shows"),
		episode("The Wire", 1, 2, "TV Shows"),
		episode("The Wire", 2, 1, "TV Shows"),
		episode("Severance", 1, 1, "TV Shows"),
	}}
	got := run(t, newWithServer(srv, mediaserver.Sections{}))
	if len(got) != 2 {
		t.Fatalf("want 2 shows, got %d", len(got))
	}
	// Shows() sorts by normalized name, so severance precedes the wire.
	if got[0].Title != "Severance" || got[1].Title != "The Wire" {
		t.Errorf("unexpected titles: %q, %q", got[0].Title, got[1].Title)
	}
	if n := got[1].Fields[entry.FieldSeriesEpisodeCount]; n != 3 {
		t.Errorf("The Wire episode count = %v, want 3", n)
	}
	if n := got[0].Fields[entry.FieldSeriesEpisodeCount]; n != 1 {
		t.Errorf("Severance episode count = %v, want 1", n)
	}
}

// TestEntryShape pins the fields series_gaps reads off an upstream show, and
// the stable URL that lets this dedup against series_tracker.
func TestEntryShape(t *testing.T) {
	srv := &stubServer{items: []mediaserver.Item{
		episode("Marvel's Daredevil", 1, 1, "TV Shows"),
	}}
	got := run(t, newWithServer(srv, mediaserver.Sections{}))
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	// Title keeps the server's spelling: a normalized key is not something
	// to put in a search query.
	if e.Title != "Marvel's Daredevil" {
		t.Errorf("Title = %q, want the server's spelling", e.Title)
	}
	// series_name is the normalized key series_gaps looks up by; the
	// apostrophe is dropped rather than becoming a word break.
	if got := e.GetString(entry.FieldSeriesName); got != "marvels daredevil" {
		t.Errorf("series_name = %q, want %q", got, "marvels daredevil")
	}
	if got, want := e.URL, "pipeliner://series/marvels%20daredevil"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if got := e.GetString(entry.FieldMediaType); got != entry.MediaTypeSeries {
		t.Errorf("media_type = %q, want %q", got, entry.MediaTypeSeries)
	}
	if got := e.GetString(entry.FieldSource); got != pluginName {
		t.Errorf("source = %q, want %q", got, pluginName)
	}
}

func TestMoviesAndUnnamedShowsIgnored(t *testing.T) {
	srv := &stubServer{items: []mediaserver.Item{
		{Type: "movie", Title: "Dune", Year: 2021, Section: "Movies"},
		episode("", 1, 1, "TV Shows"), // no show name
		episode("Andor", 1, 1, "TV Shows"),
	}}
	got := run(t, newWithServer(srv, mediaserver.Sections{}))
	if len(got) != 1 || got[0].Title != "Andor" {
		t.Errorf("want only Andor, got %d entries", len(got))
	}
}

func TestSectionsHonoured(t *testing.T) {
	srv := &stubServer{items: []mediaserver.Item{
		episode("Bluey", 1, 1, "Kids TV"),
		episode("Andor", 1, 1, "TV Shows"),
	}}
	got := run(t, newWithServer(srv, mediaserver.NewSections([]string{"TV Shows"}, nil)))
	if len(got) != 1 || got[0].Title != "Andor" {
		t.Fatalf("include filter not honoured, got %d entries", len(got))
	}

	got = run(t, newWithServer(srv, mediaserver.NewSections(nil, []string{"Kids TV"})))
	if len(got) != 1 || got[0].Title != "Andor" {
		t.Errorf("exclude filter not honoured, got %d entries", len(got))
	}
}

// TestUnreachableServerErrors: emitting nothing would be indistinguishable
// from an empty library, and a downstream backfill would then do nothing
// while the run reported success.
func TestUnreachableServerErrors(t *testing.T) {
	p := newWithServer(&stubServer{err: errors.New("connection refused")}, mediaserver.Sections{})
	if _, err := p.Generate(context.Background(), makeCtx()); err == nil {
		t.Error("want an error when the server is unreachable")
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) != 0 {
		t.Errorf("empty config should validate (plex account mode): %v", errs)
	}
	if errs := validate(map[string]any{"backend": "jellyfin"}); len(errs) != 0 {
		t.Errorf("jellyfin rejected: %v", errs)
	}
	if errs := validate(map[string]any{"backend": "kodi"}); len(errs) == 0 {
		t.Error("an unknown backend should not validate")
	}
	if errs := validate(map[string]any{
		"sections": []any{"a"}, "exclude_sections": []any{"b"},
	}); len(errs) == 0 {
		t.Error("sections and exclude_sections together should not validate")
	}
	if errs := validate(map[string]any{"frobnicate": true}); len(errs) == 0 {
		t.Error("an unknown key should not validate")
	}
}

// TestEmitsTVDBIDWhenTheServerPublishesOne passes the show's identity
// downstream, so a series_gaps fed from here resolves the library by id rather
// than by a title the provider may spell differently.
func TestEmitsTVDBIDWhenTheServerPublishesOne(t *testing.T) {
	withID := episode("Severance", 1, 1, "TV Shows")
	withID.ShowTVDBID = "371980"
	srv := &stubServer{items: []mediaserver.Item{
		withID,
		episode("Andor", 1, 1, "TV Shows"), // server published no id
	}}
	got := run(t, newWithServer(srv, mediaserver.Sections{}))
	if len(got) != 2 {
		t.Fatalf("want 2 shows, got %d", len(got))
	}
	byTitle := map[string]*entry.Entry{}
	for _, e := range got {
		byTitle[e.Title] = e
	}
	if id := byTitle["Severance"].GetString("tvdb_id"); id != "371980" {
		t.Errorf("tvdb_id = %q, want 371980", id)
	}
	// Absent rather than empty: a downstream `with`/presence check must not
	// see a field the server never supplied.
	if _, ok := byTitle["Andor"].Fields["tvdb_id"]; ok {
		t.Error("a show with no published id must not carry a tvdb_id field")
	}
}
