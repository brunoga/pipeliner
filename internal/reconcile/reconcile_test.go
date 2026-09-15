package reconcile

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/mediaserver"
	imovies "github.com/brunoga/pipeliner/internal/movies"
)

func rec(key, title string, year int, is3D bool) TrackerRecord {
	return TrackerRecord{Key: key, Record: imovies.Record{Title: title, Year: year, Is3D: is3D}}
}

func movie(title string, year int) mediaserver.Item {
	return mediaserver.Item{Type: "movie", Title: title, Year: year}
}

// The matching cases are lifted from the real forensic pass that found 49
// stuck 3D movies: these exact spellings produced false positives until the
// matcher handled them.
func TestMoviesMatching(t *testing.T) {
	sections := []mediaserver.MovieSection{
		{Name: "3D Movies", Items: []mediaserver.Item{
			movie("Deadpool & Wolverine", 2024),                // & vs "and"
			movie("Ice Age: Dawn of the Dinosaurs", 2009),      // tracker adds "3"
			movie("Avatar", 2009),
			movie("Demon Slayer -Kimetsu no Yaiba- The Movie: Mugen Train", 2020),
		}},
		{Name: "Movies", Items: []mediaserver.Item{
			movie("Inception", 2010),
		}},
	}

	records := []TrackerRecord{
		rec("deadpool and wolverine|2024|3d", "deadpool and wolverine", 2024, true),
		rec("ice age 3 dawn of the dinosaurs|2009|3d", "ice age 3 dawn of the dinosaurs", 2009, true),
		rec("avatar|2009|3d", "avatar", 2009, true),
		rec("demon slayer kimetsu no yaiba the movie mugen train|2020|3d", "demon slayer kimetsu no yaiba the movie mugen train", 2020, true),
		rec("inception|2010", "inception", 2010, false),
		// Genuinely missing:
		rec("the darkest hour|2011|3d", "the darkest hour", 2011, true),
	}

	missing := Movies(records, sections)
	if len(missing) != 1 || missing[0].Key != "the darkest hour|2011|3d" {
		keys := make([]string, len(missing))
		for i, m := range missing {
			keys[i] = m.Key
		}
		t.Errorf("want only the darkest hour missing, got %v", keys)
	}
}

// Owning the 2D copy must not mask a missing 3D record, and vice versa.
func TestMovies3DSectionSeparation(t *testing.T) {
	sections := []mediaserver.MovieSection{
		{Name: "Movies", Items: []mediaserver.Item{movie("Oppenheimer", 2023)}},
	}
	records := []TrackerRecord{
		rec("oppenheimer|2023", "oppenheimer", 2023, false),      // present in 2D
		rec("oppenheimer|2023|3d", "oppenheimer", 2023, true),    // NOT in any 3D section
	}
	missing := Movies(records, sections)
	if len(missing) != 1 || !missing[0].Is3D {
		t.Errorf("the 3D record should be missing despite the 2D copy, got %+v", missing)
	}
}

func TestMoviesYearDrift(t *testing.T) {
	sections := []mediaserver.MovieSection{
		{Name: "Movies", Items: []mediaserver.Item{
			movie("Good Boy", 2026),  // library says home-video year
			movie("Old Film", 0),     // library has no year → wildcard
		}},
	}
	records := []TrackerRecord{
		rec("good boy|2025", "good boy", 2025, false), // ±1 drift → matched
		rec("old film|1999", "old film", 1999, false), // wildcard → matched
		rec("good boy|2020", "good boy", 2020, false), // 6 years off → missing
	}
	missing := Movies(records, sections)
	if len(missing) != 1 || missing[0].Key != "good boy|2020" {
		t.Errorf("only the 6-years-off record should be missing, got %+v", missing)
	}
}

func TestMoviesSubsetDoesNotOvermatch(t *testing.T) {
	// A one-token title must not subset-match into every longer title.
	sections := []mediaserver.MovieSection{
		{Name: "Movies", Items: []mediaserver.Item{movie("X2 Global Webcast Highlights", 2003)}},
	}
	// "x2" ⊆ {"x2","global",...} — this IS a subset match by design (year
	// compatible), so it matches. Pin the behavior so a future change is
	// deliberate.
	records := []TrackerRecord{rec("x2|2003", "x2", 2003, false)}
	if missing := Movies(records, sections); len(missing) != 0 {
		t.Errorf("subset match expected, got missing %+v", missing)
	}
	// But with an incompatible year it must not match.
	records = []TrackerRecord{rec("x2|1998", "x2", 1998, false)}
	if missing := Movies(records, sections); len(missing) != 1 {
		t.Error("incompatible year must prevent the subset match")
	}
}
