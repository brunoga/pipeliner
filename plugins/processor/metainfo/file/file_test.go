package file

import (
	"context"
	"slices"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func annotateTitle(t *testing.T, title string) *entry.Entry {
	t.Helper()
	p, _ := newPlugin(nil, nil)
	e := entry.New(title, "http://x.com/a")
	if _, err := p.(plugin.ProcessorPlugin).Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	return e
}

// --- Series classification ---

func TestSeriesStandardEpisode(t *testing.T) {
	e := annotateTitle(t, "My.Show.S02E05.720p.HDTV")
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q, want %q", v, entry.MediaTypeSeries)
	}
	if v := e.GetString(entry.FieldTitle); v != "My Show" {
		t.Errorf("title: got %q, want %q", v, "My Show")
	}
	if v := e.GetString(entry.FieldSeriesEpisodeID); v != "S02E05" {
		t.Errorf("series_episode_id: got %q, want S02E05", v)
	}
	if v := e.GetInt(entry.FieldSeriesSeason); v != 2 {
		t.Errorf("series_season: got %d, want 2", v)
	}
	if v := e.GetInt(entry.FieldSeriesEpisode); v != 5 {
		t.Errorf("series_episode: got %d, want 5", v)
	}
}

func TestSeriesDoubleEpisode(t *testing.T) {
	e := annotateTitle(t, "My.Show.S01E01E02.720p.HDTV")
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q, want series", v)
	}
	if v := e.GetInt(entry.FieldSeriesDoubleEpisode); v != 2 {
		t.Errorf("series_double_episode: got %d, want 2", v)
	}
}

func TestSeriesProperFlag(t *testing.T) {
	e := annotateTitle(t, "My.Show.S01E01.PROPER.720p.HDTV")
	if v, _ := e.Get(entry.FieldSeriesProper); v != true {
		t.Errorf("series_proper: got %v, want true", v)
	}
}

func TestSeriesService(t *testing.T) {
	e := annotateTitle(t, "My.Show.S01E01.NF.1080p.WEB-DL")
	if v := e.GetString(entry.FieldSeriesService); v != "Netflix" {
		t.Errorf("series_service: got %q, want Netflix", v)
	}
}

func TestSeriesContainer(t *testing.T) {
	e := annotateTitle(t, "My.Show.S01E01.1080p.BluRay.mkv")
	if v := e.GetString("series_container"); v != "mkv" {
		t.Errorf("series_container: got %q, want mkv", v)
	}
}

func TestSeriesDateEpisode(t *testing.T) {
	e := annotateTitle(t, "Daily.Show.2023.11.15.720p.HDTV")
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q, want series", v)
	}
	if v := e.GetString(entry.FieldSeriesEpisodeID); v != "2023-11-15" {
		t.Errorf("series_episode_id: got %q, want 2023-11-15", v)
	}
}

// --- Movie classification ---

func TestMovieWithYear(t *testing.T) {
	e := annotateTitle(t, "Avengers.2012.1080p.BluRay.x264")
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeMovie {
		t.Errorf("media_type: got %q, want %q", v, entry.MediaTypeMovie)
	}
	if v := e.GetString(entry.FieldTitle); v != "Avengers" {
		t.Errorf("title: got %q, want Avengers", v)
	}
	if v := e.GetString(entry.FieldMovieTitle); v != "Avengers" {
		t.Errorf("movie_title: got %q, want Avengers", v)
	}
	if v := e.GetInt(entry.FieldVideoYear); v != 2012 {
		t.Errorf("video_year: got %d, want 2012", v)
	}
}

func TestMovieListSourceFallback(t *testing.T) {
	// A clean list-sourced title (e.g. from trakt_list) has no year or quality
	// marker, so imovies.Parse returns false. metainfo_file falls back to the
	// upstream-set video_year to classify it as a movie.
	p, _ := newPlugin(nil, nil)
	e := entry.New("Avengers", "http://x.com/a")
	e.Set(entry.FieldVideoYear, 2012)
	if _, err := p.(plugin.ProcessorPlugin).Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeMovie {
		t.Errorf("media_type: got %q, want %q (list-sourced fallback should classify as movie)", v, entry.MediaTypeMovie)
	}
	if v := e.GetString(entry.FieldMovieTitle); v != "Avengers" {
		t.Errorf("movie_title: got %q, want Avengers", v)
	}
	// video_year was already set upstream; annotateMovie writes it back via
	// SetMovieInfo. Either way, the downstream value should still be 2012.
	if v := e.GetInt(entry.FieldVideoYear); v != 2012 {
		t.Errorf("video_year: got %d, want 2012", v)
	}
}

func TestMovieListSourceFallbackPreservesDottedTitle(t *testing.T) {
	// trakt_list titles are usually clean already, but the fallback should
	// still normalise (handle dots, underscores, casing) for resilience.
	p, _ := newPlugin(nil, nil)
	e := entry.New("the.dark.knight", "http://x.com/a")
	e.Set(entry.FieldVideoYear, 2008)
	if _, err := p.(plugin.ProcessorPlugin).Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString(entry.FieldMovieTitle); v != "The Dark Knight" {
		t.Errorf("movie_title: got %q, want The Dark Knight (normalised)", v)
	}
}

func TestNoFallbackWhenYearAbsent(t *testing.T) {
	// Without video_year and without any title-derivable signal, the entry
	// stays unclassified.
	p, _ := newPlugin(nil, nil)
	e := entry.New("Avengers", "http://x.com/a")
	if _, err := p.(plugin.ProcessorPlugin).Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString(entry.FieldMediaType); v != "" {
		t.Errorf("media_type should be unset without year hint, got %q", v)
	}
}

func TestSeriesStillWinsOverFallback(t *testing.T) {
	// Even when video_year is set, an explicit episode marker keeps the
	// classification as series — the list-source fallback only kicks in when
	// neither parser matched.
	p, _ := newPlugin(nil, nil)
	e := entry.New("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	e.Set(entry.FieldVideoYear, 2012)
	if _, err := p.(plugin.ProcessorPlugin).Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q, want series", v)
	}
}

func TestSeriesWinsOverMovie(t *testing.T) {
	// "Show.2023.S01E01" matches both parsers; series must win because the
	// episode marker is unambiguous.
	e := annotateTitle(t, "Show.2023.S01E01.720p.WEB-DL")
	if v := e.GetString(entry.FieldMediaType); v != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q, want series — episode pattern must outrank year", v)
	}
	if v := e.GetString(entry.FieldMovieTitle); v != "" {
		t.Errorf("movie_title should not be set when classified as series, got %q", v)
	}
	if v := e.GetString(entry.FieldSeriesEpisodeID); v != "S01E01" {
		t.Errorf("series_episode_id: got %q, want S01E01", v)
	}
}

// --- Quality annotation ---

func TestQualityAlwaysAnnotated(t *testing.T) {
	// Series + quality.
	e := annotateTitle(t, "My.Show.S01E01.1080p.BluRay")
	if v := e.GetString(entry.FieldVideoResolution); v != "1080p" {
		t.Errorf("video_resolution: got %q, want 1080p", v)
	}
	if v := e.GetString(entry.FieldVideoSource); v != "BluRay" {
		t.Errorf("video_source: got %q, want BluRay", v)
	}
	// Movie + quality.
	e = annotateTitle(t, "Avengers.2012.720p.WEB-DL")
	if v := e.GetString(entry.FieldVideoResolution); v != "720p" {
		t.Errorf("video_resolution: got %q, want 720p", v)
	}
	if v := e.GetString(entry.FieldVideoSource); v != "WEB-DL" {
		t.Errorf("video_source: got %q, want WEB-DL", v)
	}
}

func TestQualityCodecAudio(t *testing.T) {
	e := annotateTitle(t, "Movie.2020.2160p.BluRay.x265.Atmos")
	if v := e.GetString("codec"); v != "H265" {
		t.Errorf("codec: got %q, want H265", v)
	}
	if v := e.GetString("audio"); v != "Atmos" {
		t.Errorf("audio: got %q, want Atmos", v)
	}
}

func TestQualityStructAvailable(t *testing.T) {
	// The parsed Quality struct is stored on the entry so downstream consumers
	// (premiere, series, movies, quality filter) can spec.Matches without
	// re-parsing the title.
	e := annotateTitle(t, "Movie.2020.2160p.BluRay.x265.Atmos")
	q, ok := e.Quality()
	if !ok {
		t.Fatal("Quality() returned ok=false after metainfo_file ran")
	}
	if q.Resolution == 0 || q.Source == 0 {
		t.Errorf("Quality struct missing dimensions: %+v", q)
	}
}

func TestQualityStructNotSetWhenNothingDetected(t *testing.T) {
	// A plain-text title with no quality markers must not stamp an empty
	// Quality struct (the SetQuality call is gated on q != zero).
	e := annotateTitle(t, "Just A Plain Article Title")
	if _, ok := e.Quality(); ok {
		t.Error("Quality() should return ok=false for plain-text titles")
	}
}

func TestQualityNotSetWhenAbsent(t *testing.T) {
	// Plain article-style title — no quality detectable.
	e := annotateTitle(t, "Just A Plain Article Title")
	if v := e.GetString(entry.FieldVideoResolution); v != "" {
		t.Errorf("video_resolution should be unset for plain text, got %q", v)
	}
	if v := e.GetString(entry.FieldVideoQuality); v != "" {
		t.Errorf("video_quality should be unset for plain text, got %q", v)
	}
}

// --- Other / unclassified ---

func TestPlainTextLeavesUnset(t *testing.T) {
	e := annotateTitle(t, "Just A Plain Article Title")
	if v := e.GetString(entry.FieldMediaType); v != "" {
		t.Errorf("media_type should be unset for unclassifiable title, got %q", v)
	}
	if v := e.GetString(entry.FieldTitle); v != "" {
		t.Errorf("title should not be set when nothing parsed, got %q", v)
	}
	if v := e.GetString(entry.FieldMovieTitle); v != "" {
		t.Errorf("movie_title should not be set when nothing parsed, got %q", v)
	}
}

// --- Pipeline behaviour ---

func TestRejectedEntriesSkipped(t *testing.T) {
	p, _ := newPlugin(nil, nil)
	e := entry.New("My.Show.S01E01.720p", "http://x.com/a")
	e.Reject("test")
	if _, err := p.(plugin.ProcessorPlugin).Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString(entry.FieldMediaType); v != "" {
		t.Errorf("rejected entry should not be annotated, got media_type=%q", v)
	}
}

func TestProcessPassesEntriesThrough(t *testing.T) {
	p, _ := newPlugin(nil, nil)
	in := []*entry.Entry{
		entry.New("My.Show.S01E01.720p", "http://x.com/a"),
		entry.New("Avengers.2012.1080p.BluRay", "http://x.com/b"),
		entry.New("Plain Article Title", "http://x.com/c"),
	}
	out, err := p.(plugin.ProcessorPlugin).Process(context.Background(), makeCtx(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Errorf("Process must pass all entries through; got %d, want %d", len(out), len(in))
	}
}

// --- Registration ---

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("metainfo_file")
	if !ok {
		t.Fatal("metainfo_file not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("role: got %v, want processor", d.Role)
	}
	// MayProduce must include media_type, the classification marker.
	if !slices.Contains(d.MayProduce, entry.FieldMediaType) {
		t.Error("MayProduce must include media_type")
	}
}
