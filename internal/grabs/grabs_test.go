package grabs

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/quality"
)

// memBucket is an in-memory bucket for tests.
type memBucket struct {
	data map[string]Record
}

func newMemBucket() *memBucket { return &memBucket{data: map[string]Record{}} }

func (b *memBucket) Put(key string, value any) error {
	b.data[key] = value.(Record)
	return nil
}

func (b *memBucket) Get(key string, dest any) (bool, error) {
	rec, ok := b.data[key]
	if !ok {
		return false, nil
	}
	*dest.(*Record) = rec
	return true, nil
}

func (b *memBucket) Delete(key string) error {
	delete(b.data, key)
	return nil
}

func TestStoreRoundTripLowercasesHash(t *testing.T) {
	s := NewStore(newMemBucket())

	rec := Record{URL: "https://x.example/release.torrent", Title: "Show.S01E01"}
	if err := s.Put("ABCDEF0123456789ABCDEF0123456789ABCDEF01", rec); err != nil {
		t.Fatal(err)
	}

	got, ok := s.Get("abcdef0123456789abcdef0123456789abcdef01")
	if !ok {
		t.Fatal("Get by lowercase hash should hit")
	}
	if got.URL != rec.URL || got.Title != rec.Title {
		t.Errorf("got %+v", got)
	}
	if got.AddedAt.IsZero() {
		t.Error("AddedAt should be auto-filled")
	}

	// Mixed-case lookup also hits.
	if _, ok := s.Get("AbCdEf0123456789abcdef0123456789abcdef01"); !ok {
		t.Error("mixed-case Get should hit")
	}

	if err := s.Delete("ABCDEF0123456789ABCDEF0123456789ABCDEF01"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("abcdef0123456789abcdef0123456789abcdef01"); ok {
		t.Error("Get should miss after Delete")
	}
}

func TestFromEntryCapturesTrackerKeys(t *testing.T) {
	e := entry.New("Show S01E03 720p", "https://x.example/r/42.torrent")
	e.Set(entry.FieldSeriesTrackerName, "show")
	e.Set(entry.FieldSeriesEpisodeID, "S01E03")

	rec := FromEntry(e, "tv-shows")
	if rec.URL != "https://x.example/r/42.torrent" {
		t.Errorf("URL = %q", rec.URL)
	}
	if rec.Title != "Show S01E03 720p" {
		t.Errorf("Title = %q", rec.Title)
	}
	if rec.Task != "tv-shows" {
		t.Errorf("Task = %q", rec.Task)
	}
	if rec.SeriesName != "show" || rec.EpisodeID != "S01E03" {
		t.Errorf("series key = %q/%q", rec.SeriesName, rec.EpisodeID)
	}
	if rec.MovieTitle != "" {
		t.Errorf("MovieTitle should be empty, got %q", rec.MovieTitle)
	}

	m := entry.New("Dune Part Two 2024 1080p", "magnet:?xt=urn:btih:bbbb")
	m.Set(entry.FieldMoviesTrackerTitle, "dune part two")
	m.Set(entry.FieldVideoYear, 2024)
	m.Set(entry.FieldVideoIs3D, true)

	mrec := FromEntry(m, "movies")
	if mrec.MovieTitle != "dune part two" || mrec.MovieYear != 2024 || !mrec.MovieIs3D {
		t.Errorf("movie key = %+v", mrec)
	}
}

func TestHashForEntry(t *testing.T) {
	// Field wins and is lowercased.
	e := entry.New("t", "https://x.example/a.torrent")
	e.Set(entry.FieldTorrentInfoHash, "ABCDEF0123456789ABCDEF0123456789ABCDEF01")
	if h := HashForEntry(e); h != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("field hash = %q", h)
	}

	// Magnet URL fallback.
	m := entry.New("t", "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=x")
	if h := HashForEntry(m); h != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("magnet hash = %q", h)
	}

	// Bare .torrent URL with no field: unknown.
	u := entry.New("t", "https://x.example/a.torrent")
	if h := HashForEntry(u); h != "" {
		t.Errorf("expected empty hash, got %q", h)
	}
}

// TestFromEntryUsesStampedTrackerKey is the regression guard for grab records
// that could not un-track anything: the metadata plugins overwrite video_year
// and may sit between the movies filter and the torrent sink, so reading
// video_year here recorded a key that did not match the tracker row.
func TestFromEntryUsesStampedTrackerKey(t *testing.T) {
	e := entry.New("Aladdin 2019 1080p BluRay x264-DON", "https://example.test/a.torrent")
	e.Set(entry.FieldMoviesTrackerTitle, "aladdin")
	e.Set(entry.FieldMoviesTrackerYear, 2019)
	e.Set(entry.FieldMoviesTrackerIs3D, false)
	// metainfo_tmdb matched the 1992 animated film and rewrote video_year.
	e.Set(entry.FieldVideoYear, 1992)
	e.SetQuality(quality.Parse("1080p bluray x264"))

	rec := FromEntry(e, "movies")
	if rec.MovieTitle != "aladdin" {
		t.Errorf("MovieTitle = %q", rec.MovieTitle)
	}
	if rec.MovieYear != 2019 {
		t.Errorf("MovieYear = %d, want the year the filter decided with (2019)", rec.MovieYear)
	}
	if rec.Quality != quality.Parse("1080p bluray x264") {
		t.Errorf("Quality = %s, want the grabbed release's quality", rec.Quality)
	}
}

// Entries stamped by an older build carry no tracker year; the record falls
// back to video_year rather than recording year 0.
func TestFromEntryFallsBackToVideoYear(t *testing.T) {
	e := entry.New("Dune 2021 2160p", "https://example.test/d.torrent")
	e.Set(entry.FieldMoviesTrackerTitle, "dune")
	e.Set(entry.FieldVideoYear, 2021)
	e.Set(entry.FieldVideoIs3D, true)

	rec := FromEntry(e, "movies")
	if rec.MovieYear != 2021 || !rec.MovieIs3D {
		t.Errorf("fallback key = (%d, 3d=%v), want (2021, 3d=true)", rec.MovieYear, rec.MovieIs3D)
	}
}

// An entry that never passed a movies filter has no movie key to record.
func TestFromEntryWithoutMoviesFilter(t *testing.T) {
	e := entry.New("Some.Show.S01E01", "https://example.test/s.torrent")
	e.Set(entry.FieldVideoYear, 2024)

	rec := FromEntry(e, "tv")
	if rec.MovieTitle != "" || rec.MovieYear != 0 {
		t.Errorf("no movies filter ran, so no movie key: %+v", rec)
	}
}

// The tracker bucket travels on the grab record so failed-grab recovery rolls
// back the tracker the movies filter actually wrote to.
func TestFromEntryCarriesTrackerBucket(t *testing.T) {
	e := entry.New("Inception 2010 1080p BluRay x264", "https://example.test/i.torrent")
	e.Set(entry.FieldMoviesTrackerTitle, "inception")
	e.Set(entry.FieldMoviesTrackerYear, 2010)
	e.Set(entry.FieldMoviesTrackerBucket, "movies:3d-mvc-harvest")
	if got := FromEntry(e, "3d-mvc-harvest").MovieBucket; got != "movies:3d-mvc-harvest" {
		t.Errorf("MovieBucket = %q", got)
	}

	// An entry from a shared-tracker node stamps the shared bucket; one that
	// never passed a movies filter stamps nothing.
	e2 := entry.New("Some.Show.S01E01", "https://example.test/s.torrent")
	if got := FromEntry(e2, "tv").MovieBucket; got != "" {
		t.Errorf("MovieBucket = %q, want empty", got)
	}
}
