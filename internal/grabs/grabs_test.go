package grabs

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
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
