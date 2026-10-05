package movies

import (
	"testing"
	"time"

	"github.com/brunoga/pipeliner/quality"
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

// TestTrackerIsSeenStoredYearZero covers the reverse of the yearless-filename
// case: the record was stored with year 0 (the first download happened before
// the year could be enriched), and a later release carries a real year. IsSeen
// must still return true — a stored year of 0 means "unknown", so it should not
// be excluded by the ±1 drift check that LatestNearYear applies. Without this,
// the movie is re-downloaded (and, worse, never seen as a quality upgrade).
func TestTrackerIsSeenStoredYearZero(t *testing.T) {
	tr := NewTracker(newMemBucket())

	// First download recorded no year (enrichment hadn't resolved it yet).
	if err := tr.Mark(Record{Title: "Peaky Blinders The Immortal Man", Year: 0}); err != nil {
		t.Fatal(err)
	}

	// A later release names it 2026 — must still be gated against the year-0 record.
	if !tr.IsSeen("Peaky Blinders The Immortal Man", 2026, false) {
		t.Error("IsSeen(year=2026) should return true when a year-0 record exists")
	}
	// The upgrade path (LatestNearYear) must also find the year-0 record.
	if _, ok := tr.LatestNearYear("Peaky Blinders The Immortal Man", 2026, false); !ok {
		t.Error("LatestNearYear(2026) should find the year-0 record")
	}
	// 3D remains independent — a non-3D year-0 record must not gate a 3D entry.
	if tr.IsSeen("Peaky Blinders The Immortal Man", 2026, true) {
		t.Error("IsSeen(year=2026, 3D) should return false when only a non-3D record exists")
	}
}

// TestTrackerIsSeenYearDrift covers theatrical vs. home-video release-year
// drift: a film stored under one year must still be detected when the
// incoming release names it by the adjacent year. The motivating case is
// Good Boy (2025 theatrical / 2026 Blu-ray) — without ±1 tolerance the
// 1080p Blu-ray rip gets accepted as a brand-new movie even though a
// 2160p copy is already tracked.
func TestTrackerIsSeenYearDrift(t *testing.T) {
	tr := NewTracker(newMemBucket())
	if err := tr.Mark(Record{Title: "Good Boy", Year: 2026}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		year int
		want bool
	}{
		{2025, true},  // ±1 drift below
		{2026, true},  // exact
		{2027, true},  // ±1 drift above
		{2024, false}, // out of tolerance
		{2028, false}, // out of tolerance
	}
	for _, c := range cases {
		if got := tr.IsSeen("Good Boy", c.year, false); got != c.want {
			t.Errorf("IsSeen(year=%d) = %v, want %v", c.year, got, c.want)
		}
	}
}

func TestTrackerLatestNearYear(t *testing.T) {
	tr := NewTracker(newMemBucket())
	if err := tr.Mark(Record{Title: "Good Boy", Year: 2026}); err != nil {
		t.Fatal(err)
	}

	if rec, ok := tr.LatestNearYear("Good Boy", 2025, false); !ok || rec.Year != 2026 {
		t.Errorf("LatestNearYear(2025): got ok=%v year=%d, want ok=true year=2026", ok, recYear(rec))
	}
	if _, ok := tr.LatestNearYear("Good Boy", 2024, false); ok {
		t.Error("LatestNearYear(2024): want ok=false (out of ±1 tolerance)")
	}

	// Among multiple in-tolerance records, the most recent DownloadedAt wins.
	older := time.Now().Add(-48 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	if err := tr.Mark(Record{Title: "Toy Story", Year: 1995, DownloadedAt: older}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Mark(Record{Title: "Toy Story", Year: 1996, DownloadedAt: newer}); err != nil {
		t.Fatal(err)
	}
	if rec, ok := tr.LatestNearYear("Toy Story", 1995, false); !ok || rec.Year != 1996 {
		t.Errorf("LatestNearYear should pick newest DownloadedAt within tolerance, got year=%d", recYear(rec))
	}
}

func recYear(r *Record) int {
	if r == nil {
		return 0
	}
	return r.Year
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

// TestMarkKeepsPreviousDownload covers the one-level undo Mark maintains: the
// record it replaces is carried along so a failed grab can be rolled back
// instead of erasing the film's history.
func TestMarkKeepsPreviousDownload(t *testing.T) {
	tr := NewTracker(newMemBucket())
	first := quality.Parse("1080p bluray x264")
	second := quality.Parse("2160p bluray x265 dolby vision")

	if err := tr.Mark(Record{Title: "dune", Year: 2021, Quality: first}); err != nil {
		t.Fatal(err)
	}
	rec, ok := tr.Latest("dune", false)
	if !ok {
		t.Fatal("first download should be tracked")
	}
	if rec.Prev != nil {
		t.Errorf("a first download has nothing to roll back to, got %+v", rec.Prev)
	}

	if err := tr.Mark(Record{Title: "dune", Year: 2021, Quality: second}); err != nil {
		t.Fatal(err)
	}
	rec, _ = tr.Latest("dune", false)
	if rec.Quality != second {
		t.Errorf("current quality = %s, want %s", rec.Quality, second)
	}
	if rec.Prev == nil || rec.Prev.Quality != first {
		t.Fatalf("Prev should hold the superseded download, got %+v", rec.Prev)
	}
}

func TestUntrackGrab(t *testing.T) {
	low := quality.Parse("1080p web-dl x264")
	high := quality.Parse("2160p bluray x265 atmos")

	t.Run("no record is a no-op", func(t *testing.T) {
		tr := NewTracker(newMemBucket())
		got, err := tr.UntrackGrab("dune", 2021, false, low, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != UntrackNoRecord {
			t.Errorf("outcome = %v, want UntrackNoRecord", got)
		}
	})

	t.Run("only download on record is deleted", func(t *testing.T) {
		tr := NewTracker(newMemBucket())
		if err := tr.Mark(Record{Title: "dune", Year: 2021, Quality: low}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.UntrackGrab("dune", 2021, false, low, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != UntrackDeleted {
			t.Errorf("outcome = %v, want UntrackDeleted", got)
		}
		if tr.IsSeen("dune", 2021, false) {
			t.Error("record should be gone so another release can be tried")
		}
	})

	// The regression this exists for: a failed upgrade used to delete the
	// record outright, which also forgot the good copy already in the library
	// and re-downloaded the film — often at worse quality than it had.
	t.Run("failed upgrade rolls back to the previous download", func(t *testing.T) {
		tr := NewTracker(newMemBucket())
		if err := tr.Mark(Record{Title: "dune", Year: 2021, Quality: low}); err != nil {
			t.Fatal(err)
		}
		if err := tr.Mark(Record{Title: "dune", Year: 2021, Quality: high, Repack: true}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.UntrackGrab("dune", 2021, false, high, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != UntrackRestored {
			t.Fatalf("outcome = %v, want UntrackRestored", got)
		}
		rec, ok := tr.Latest("dune", false)
		if !ok {
			t.Fatal("the earlier download must stay tracked")
		}
		if rec.Quality != low {
			t.Errorf("restored quality = %s, want %s", rec.Quality, low)
		}
		if rec.Repack {
			t.Error("restored record must carry the earlier download's repack flag")
		}
		if rec.Prev != nil {
			t.Error("rollback consumes the undo level")
		}
	})

	t.Run("record from a later download is left alone", func(t *testing.T) {
		tr := NewTracker(newMemBucket())
		if err := tr.Mark(Record{Title: "dune", Year: 2021, Quality: high}); err != nil {
			t.Fatal(err)
		}
		// An older torrent dies after a better copy was already recorded.
		got, err := tr.UntrackGrab("dune", 2021, false, low, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != UntrackStale {
			t.Fatalf("outcome = %v, want UntrackStale", got)
		}
		rec, _ := tr.Latest("dune", false)
		if rec.Quality != high {
			t.Errorf("quality = %s, want the later download %s", rec.Quality, high)
		}
	})

	t.Run("grab record without quality still rolls back", func(t *testing.T) {
		tr := NewTracker(newMemBucket())
		if err := tr.Mark(Record{Title: "dune", Year: 2021, Quality: low}); err != nil {
			t.Fatal(err)
		}
		if err := tr.Mark(Record{Title: "dune", Year: 2021, Quality: high}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.UntrackGrab("dune", 2021, false, quality.Quality{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if got != UntrackRestored {
			t.Fatalf("outcome = %v, want UntrackRestored", got)
		}
		rec, _ := tr.Latest("dune", false)
		if rec.Quality != low {
			t.Errorf("restored quality = %s, want %s", rec.Quality, low)
		}
	})
}
