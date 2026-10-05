package untrack

import (
	"testing"
	"time"
)

// memBucket is an in-memory bucket for testing.
type memBucket struct {
	data map[string]Record
	err  error
}

func newMemBucket() *memBucket { return &memBucket{data: map[string]Record{}} }

func (b *memBucket) Put(key string, value any) error {
	if b.err != nil {
		return b.err
	}
	b.data[key] = value.(Record)
	return nil
}

func (b *memBucket) Get(key string, dest any) (bool, error) {
	if b.err != nil {
		return false, b.err
	}
	r, ok := b.data[key]
	if !ok {
		return false, nil
	}
	*(dest.(*Record)) = r
	return true, nil
}

func TestMovieKey(t *testing.T) {
	tests := []struct {
		title string
		year  int
		is3D  bool
		want  string
	}{
		{"dune", 2021, false, "movie|dune|2021"},
		{"Dune", 2021, false, "movie|dune|2021"},
		{"dune", 2021, true, "movie|dune|2021|3d"},
		{"dune", 0, false, "movie|dune|0"},
	}
	for _, tt := range tests {
		if got := MovieKey(tt.title, tt.year, tt.is3D); got != tt.want {
			t.Errorf("MovieKey(%q,%d,%v) = %q, want %q", tt.title, tt.year, tt.is3D, got, tt.want)
		}
	}
	// 3D and non-3D are independent holds, same as the tracker's own identity.
	if MovieKey("dune", 2021, true) == MovieKey("dune", 2021, false) {
		t.Error("3D and 2D keys must differ")
	}
}

func TestRemaining(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	t.Run("unmarked key is never held", func(t *testing.T) {
		s := NewStore(newMemBucket())
		if _, held := s.Remaining(MovieKey("dune", 2021, false), 6*time.Hour, now); held {
			t.Error("nothing was un-tracked, so nothing is held")
		}
	})

	t.Run("inside the window", func(t *testing.T) {
		s := NewStore(newMemBucket())
		if err := s.Mark(MovieKey("dune", 2021, false),
			Record{At: now.Add(-2 * time.Hour), Release: "Dune 2021 1080p", Reason: "stalled"}); err != nil {
			t.Fatal(err)
		}
		left, held := s.Remaining(MovieKey("dune", 2021, false), 6*time.Hour, now)
		if !held {
			t.Fatal("2h into a 6h window should still be held")
		}
		if left != 4*time.Hour {
			t.Errorf("left = %s, want 4h", left)
		}
	})

	t.Run("window elapsed", func(t *testing.T) {
		s := NewStore(newMemBucket())
		if err := s.Mark(MovieKey("dune", 2021, false), Record{At: now.Add(-7 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, held := s.Remaining(MovieKey("dune", 2021, false), 6*time.Hour, now); held {
			t.Error("7h into a 6h window should no longer be held")
		}
	})

	// A zero window is how the config disables the hold entirely.
	t.Run("zero window disables the hold", func(t *testing.T) {
		s := NewStore(newMemBucket())
		if err := s.Mark(MovieKey("dune", 2021, false), Record{At: now}); err != nil {
			t.Fatal(err)
		}
		if _, held := s.Remaining(MovieKey("dune", 2021, false), 0, now); held {
			t.Error("a zero window must never hold")
		}
	})

	t.Run("store error does not hold", func(t *testing.T) {
		b := newMemBucket()
		b.err = errFake{}
		s := NewStore(b)
		if _, held := s.Remaining(MovieKey("dune", 2021, false), 6*time.Hour, now); held {
			t.Error("an unreadable marker must not block downloads")
		}
	})
}

func TestMarkDefaultsToNow(t *testing.T) {
	s := NewStore(newMemBucket())
	before := time.Now()
	if err := s.Mark("movie|dune|2021", Record{Release: "Dune 2021"}); err != nil {
		t.Fatal(err)
	}
	rec, ok := s.Last("movie|dune|2021")
	if !ok {
		t.Fatal("marker should be stored")
	}
	if rec.At.Before(before) {
		t.Errorf("At = %s, should default to now", rec.At)
	}
	if rec.Release != "Dune 2021" {
		t.Errorf("Release = %q, want the failed release name", rec.Release)
	}
}

type errFake struct{}

func (errFake) Error() string { return "bucket unavailable" }

func TestEpisodeKey(t *testing.T) {
	tests := []struct {
		show, ep, want string
	}{
		{"severance", "S02E10", "episode|severance|S02E10"},
		{"Severance", "s02e10", "episode|severance|S02E10"},
		{"show", "S01E01E02", "episode|show|S01E01E02"},
		{"show", "2023-11-15", "episode|show|2023-11-15"},
	}
	for _, tt := range tests {
		if got := EpisodeKey(tt.show, tt.ep); got != tt.want {
			t.Errorf("EpisodeKey(%q,%q) = %q, want %q", tt.show, tt.ep, got, tt.want)
		}
	}
	// Episodes and movies share the bucket, so their keys must not collide.
	if EpisodeKey("dune", "S01E01") == MovieKey("dune", 2021, false) {
		t.Error("episode and movie keys must differ")
	}
}

func TestOutcomeString(t *testing.T) {
	for _, tt := range []struct {
		o    Outcome
		want string
	}{
		{NoRecord, "no record"},
		{Stale, "left (record is from a later download)"},
		{Restored, "rolled back to previous download"},
		{Deleted, "deleted"},
	} {
		if got := tt.o.String(); got != tt.want {
			t.Errorf("Outcome(%d).String() = %q, want %q", tt.o, got, tt.want)
		}
	}
}
