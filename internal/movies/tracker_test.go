package movies

import (
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
)

// memBucket is an in-memory bucket for testing.
type memBucket struct {
	data map[string]Record
}

func newMemBucket() *memBucket { return &memBucket{data: map[string]Record{}} }

func (b *memBucket) Put(key string, value any) error {
	b.data[key] = value.(Record)
	return nil
}
func (b *memBucket) Get(key string, dest any) (bool, error) {
	r, ok := b.data[key]
	if !ok {
		return false, nil
	}
	*(dest.(*Record)) = r
	return true, nil
}
func (b *memBucket) Delete(key string) error { delete(b.data, key); return nil }
func (b *memBucket) Keys() ([]string, error) {
	keys := make([]string, 0, len(b.data))
	for k := range b.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func TestTrackerIsSeen(t *testing.T) {
	tr := NewTracker(newMemBucket())
	if tr.IsSeen("The Matrix", 1999, false) {
		t.Fatal("should not be seen initially")
	}
	if err := tr.Mark(Record{Title: "The Matrix", Year: 1999, Quality: quality.Quality{}}); err != nil {
		t.Fatal(err)
	}
	if !tr.IsSeen("The Matrix", 1999, false) {
		t.Fatal("should be seen after mark")
	}
}

func TestTrackerForget(t *testing.T) {
	tr := NewTracker(newMemBucket())
	if err := tr.Mark(Record{Title: "Inception", Year: 2010}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Forget("Inception", 2010, false); err != nil {
		t.Fatal(err)
	}
	if tr.IsSeen("Inception", 2010, false) {
		t.Fatal("should not be seen after forget")
	}
}

func TestTrackerLatest(t *testing.T) {
	tr := NewTracker(newMemBucket())
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)

	if err := tr.Mark(Record{Title: "The Matrix", Year: 1999, DownloadedAt: old}); err != nil {
		t.Fatal(err)
	}
	// different year, same title
	if err := tr.Mark(Record{Title: "The Matrix", Year: 2021, DownloadedAt: recent}); err != nil {
		t.Fatal(err)
	}

	rec, ok := tr.Latest("The Matrix", false)
	if !ok {
		t.Fatal("expected a latest record")
	}
	if rec.Year != 2021 {
		t.Errorf("latest year: got %d, want 2021", rec.Year)
	}
}

func TestTrackerLatestMissing(t *testing.T) {
	tr := NewTracker(newMemBucket())
	_, ok := tr.Latest("Unknown Movie", false)
	if ok {
		t.Fatal("should return false for unknown title")
	}
}

// TestTrackerIsSeenYearlessFilename covers the case where the release filename
// has no year (parsed year=0) but the record was stored with a real year sourced
// from TMDb enrichment. IsSeen must still return true so the movie is not
// repeatedly re-accepted on every pipeline run.
func TestTrackerIsSeenYearlessFilename(t *testing.T) {
	tr := NewTracker(newMemBucket())

	// Learn stored the record with the real year (from TMDb).
	if err := tr.Mark(Record{Title: "Peaky Blinders The Immortal Man", Year: 2025, Is3D: true}); err != nil {
		t.Fatal(err)
	}

	// Filter sees year=0 (not in filename) — must still be gated.
	if !tr.IsSeen("Peaky Blinders The Immortal Man", 0, true) {
		t.Error("IsSeen(year=0) should return true when record exists with real year")
	}
	// Non-3D must remain independent.
	if tr.IsSeen("Peaky Blinders The Immortal Man", 0, false) {
		t.Error("IsSeen(year=0, non-3D) should return false when only 3D was marked")
	}
	// Exact year match must still work.
	if !tr.IsSeen("Peaky Blinders The Immortal Man", 2025, true) {
		t.Error("IsSeen(year=2025) should return true")
	}
}

func TestTrackerSeparates3DAndNon3D(t *testing.T) {
	tr := NewTracker(newMemBucket())

	if err := tr.Mark(Record{Title: "Avatar", Year: 2009, Is3D: false}); err != nil {
		t.Fatal(err)
	}
	if !tr.IsSeen("Avatar", 2009, false) {
		t.Error("non-3D should be seen after marking non-3D")
	}
	if tr.IsSeen("Avatar", 2009, true) {
		t.Error("3D should not be seen when only non-3D was marked")
	}

	if err := tr.Mark(Record{Title: "Avatar", Year: 2009, Is3D: true}); err != nil {
		t.Fatal(err)
	}
	if !tr.IsSeen("Avatar", 2009, true) {
		t.Error("3D should be seen after marking 3D")
	}
	if !tr.IsSeen("Avatar", 2009, false) {
		t.Error("non-3D should still be seen after also marking 3D")
	}
}
