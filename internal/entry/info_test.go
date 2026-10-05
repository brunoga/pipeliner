package entry

import "testing"

// TestMoviesTrackerKey covers the stamped movies tracker key. It exists
// because video_year is not stable across a run: the metadata plugins
// overwrite it with the year of whichever title they matched, so the key must
// be captured at decision time rather than re-read at commit time.
func TestMoviesTrackerKey(t *testing.T) {
	t.Run("stamped key wins over a rewritten video_year", func(t *testing.T) {
		e := New("Aladdin 2019 1080p BluRay", "http://x/1")
		e.Set(FieldMoviesTrackerTitle, "aladdin")
		e.Set(FieldMoviesTrackerYear, 2019)
		e.Set(FieldMoviesTrackerIs3D, false)
		e.Set(FieldVideoYear, 1992) // metainfo_tmdb picked the animated film
		e.Set(FieldVideoIs3D, true)

		title, year, is3D, ok := e.MoviesTrackerKey()
		if !ok {
			t.Fatal("ok should be true when a matched title is stamped")
		}
		if title != "aladdin" || year != 2019 || is3D {
			t.Errorf("key = (%q, %d, %v), want (aladdin, 2019, false)", title, year, is3D)
		}
	})

	t.Run("falls back to video fields when not stamped", func(t *testing.T) {
		e := New("Avatar 2009 3D", "http://x/2")
		e.Set(FieldMoviesTrackerTitle, "avatar")
		e.Set(FieldVideoYear, 2009)
		e.Set(FieldVideoIs3D, true)

		title, year, is3D, ok := e.MoviesTrackerKey()
		if !ok || title != "avatar" || year != 2009 || !is3D {
			t.Errorf("key = (%q, %d, %v, ok=%v), want (avatar, 2009, true, true)", title, year, is3D, ok)
		}
	})

	t.Run("a stamped year of zero is honoured", func(t *testing.T) {
		// A release with no year in its name decided with year 0; enrichment
		// later supplies one. The record must stay on the key used.
		e := New("Some Film 1080p", "http://x/3")
		e.Set(FieldMoviesTrackerTitle, "some film")
		e.Set(FieldMoviesTrackerYear, 0)
		e.Set(FieldVideoYear, 2024)

		if _, year, _, _ := e.MoviesTrackerKey(); year != 0 {
			t.Errorf("year = %d, want the stamped 0", year)
		}
	})

	t.Run("no movies filter means nothing to track", func(t *testing.T) {
		e := New("Some.Show.S01E01", "http://x/4")
		e.Set(FieldVideoYear, 2024)
		if _, _, _, ok := e.MoviesTrackerKey(); ok {
			t.Error("ok should be false without a matched title")
		}
	})
}
