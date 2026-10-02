package store

import (
	"encoding/json"
	"testing"
)

func grabSeriesName(t *testing.T, s *SQLiteStore, hash string) string {
	t.Helper()
	raw, ok := get(t, s, "grabs", hash)
	if !ok {
		t.Fatalf("grab %q missing", hash)
	}
	var g struct {
		SeriesName string `json:"series_name"`
	}
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatalf("unmarshal grab %q: %v", hash, err)
	}
	return g.SeriesName
}

func grab(title, epID string) string {
	return `{"url":"https://indexer.invalid/dl/x","title":"` + title +
		`","task":"tvshows-discover","added_at":"2026-10-01T08:00:03Z","episode_id":"` + epID + `"}`
}

func TestBackfillGrabSeriesNamesTheTrackedShow(t *testing.T) {
	s := seedBare(t)
	put(t, s, "series", "fightland|S01E01",
		rec("fightland", "S01E01", "2026-07-31T12:00:01Z", 5, 8, 3, 2, 0))
	put(t, s, "series", "the paper 2025|S01E01",
		rec("the paper 2025", "S01E01", "2026-08-04T02:00:00Z", 5, 8, 3, 2, 0))
	// Dotted separators, and a title whose show name carries the year.
	put(t, s, "grabs", "h1", grab("Fightland.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-TRB", "S01E01"))
	put(t, s, "grabs", "h2", grab("The Paper 2025 S01E01 Pilot REPACK 1080p PCOK WEB DL DDP5 1 H 264", "S01E01"))

	if err := s.runMigrations(migrationsUpTo(6)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if got := grabSeriesName(t, s, "h1"); got != "fightland" {
		t.Errorf("h1 series_name = %q, want %q", got, "fightland")
	}
	if got := grabSeriesName(t, s, "h2"); got != "the paper 2025" {
		t.Errorf("h2 series_name = %q, want %q", got, "the paper 2025")
	}
}

func TestBackfillGrabSeriesMatchesAcrossTheYearSuffix(t *testing.T) {
	s := seedBare(t)
	// The tracker key carries the premiere year; the release title does not.
	// Matching on the base name is what bridges them.
	put(t, s, "series", "east of eden 2026|S01E01",
		rec("east of eden 2026", "S01E01", "2026-10-01T08:00:05Z", 6, 8, 4, 0, 2))
	put(t, s, "grabs", "h1", grab("East of Eden S01E01 MULTI 1080p WEB X264-HiggsBoson", "S01E01"))
	// And the reverse: the title names the year, the key does not.
	put(t, s, "series", "youth|S01E01",
		rec("youth", "S01E01", "2026-09-21T03:00:02Z", 6, 8, 4, 6, 0))
	put(t, s, "grabs", "h2", grab("Youth 2026 S01E01 Lockjaw 2160p HMAX WEB-DL DD 5 1 Atmos H 265-FLUX", "S01E01"))

	if err := s.runMigrations(migrationsUpTo(6)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if got := grabSeriesName(t, s, "h1"); got != "east of eden 2026" {
		t.Errorf("h1 series_name = %q, want the year-carrying key", got)
	}
	if got := grabSeriesName(t, s, "h2"); got != "youth" {
		t.Errorf("h2 series_name = %q, want %q", got, "youth")
	}
}

func TestBackfillGrabSeriesLeavesUnresolvableGrabsAlone(t *testing.T) {
	s := seedBare(t)
	put(t, s, "series", "neagley|S01E01",
		rec("neagley", "S01E01", "2026-09-16T11:00:01Z", 4, 8, 3, 0, 0))
	// No tracker record: nothing to un-track, so writing a name would only
	// make mark_failed delete nothing while appearing to have worked.
	put(t, s, "grabs", "gone", grab("Some Cancelled Show S01E01 1080p WEB H264-NONE", "S01E01"))
	// The tracked show has no record for this episode.
	put(t, s, "grabs", "otherep", grab("Neagley S01E07 720p WEB H264-SYLiX", "S01E07"))
	// No episode marker in the title at all.
	put(t, s, "grabs", "nomarker", grab("Neagley Complete Season 1080p WEB H264", "S01E01"))

	if err := s.runMigrations(migrationsUpTo(6)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	for _, h := range []string{"gone", "otherep", "nomarker"} {
		if got := grabSeriesName(t, s, h); got != "" {
			t.Errorf("%s series_name = %q, want it left empty", h, got)
		}
	}
}

func TestBackfillGrabSeriesSkipsAmbiguousShows(t *testing.T) {
	s := seedBare(t)
	// Two year spellings for one base: migration 5 refuses to merge these, so
	// the backfill has two candidate keys for the episode and must not guess.
	put(t, s, "series", "the office 2001|S01E01",
		rec("the office 2001", "S01E01", "2026-01-01T00:00:00Z", 5, 8, 3, 0, 0))
	put(t, s, "series", "the office 2005|S01E01",
		rec("the office 2005", "S01E01", "2026-01-02T00:00:00Z", 5, 8, 3, 0, 0))
	put(t, s, "grabs", "h1", grab("The Office S01E01 1080p WEB H264-X", "S01E01"))

	if err := s.runMigrations(migrationsUpTo(6)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if got := grabSeriesName(t, s, "h1"); got != "" {
		t.Errorf("series_name = %q, want it left empty for an ambiguous show", got)
	}
}

func TestBackfillGrabSeriesLeavesCompleteRecordsUntouched(t *testing.T) {
	s := seedBare(t)
	put(t, s, "series", "last seen|S01E03",
		rec("last seen", "S01E03", "2026-09-16T03:15:19Z", 5, 8, 3, 6, 0))
	// Already named: must keep the name it was written with, even though the
	// title would resolve to the same show.
	put(t, s, "grabs", "named",
		`{"url":"https://indexer.invalid/dl/x","title":"Last Seen S01E03 The Lie 1080p ATVP WEB-DL","task":"tvshows-favorites","added_at":"2026-09-16T03:15:19Z","series_name":"last seen","episode_id":"S01E03"}`)
	// A movie grab has no episode ID and must not be considered at all.
	put(t, s, "grabs", "movie",
		`{"url":"https://indexer.invalid/dl/y","title":"Blade Runner 2049 2017 2160p UHD BluRay","task":"movies","added_at":"2026-07-25T20:10:04Z","movie_title":"blade runner 2049","movie_year":2017}`)

	if err := s.runMigrations(migrationsUpTo(6)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if got := grabSeriesName(t, s, "named"); got != "last seen" {
		t.Errorf("named grab series_name = %q, want it unchanged", got)
	}
	raw, _ := get(t, s, "grabs", "movie")
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal movie grab: %v", err)
	}
	if _, ok := m["series_name"]; ok {
		t.Error("movie grab gained a series_name")
	}
	if _, ok := m["movie_title"]; !ok {
		t.Error("movie grab lost movie_title")
	}
}

// TestBackfillGrabSeriesNeedsTheMergeFirst pins the ordering dependency
// between the two migrations. Both grabs name the same show, spelled with and
// without the premiere year; until the merge collapses the two tracker keys,
// each title has two candidate keys and neither can be resolved.
func TestBackfillGrabSeriesNeedsTheMergeFirst(t *testing.T) {
	seed := func(t *testing.T) *SQLiteStore {
		s := seedBare(t)
		put(t, s, "series", "east of eden 2026|S01E01",
			rec("east of eden 2026", "S01E01", "2026-10-01T08:00:05.385Z", 6, 8, 4, 0, 2))
		put(t, s, "series", "east of eden|S01E01",
			rec("east of eden", "S01E01", "2026-10-01T08:00:05.403Z", 5, 8, 3, 0, 0))
		put(t, s, "grabs", "h1", grab("East of Eden 2026 S01E01 HDR 2160p WEB h265-ETHEL", "S01E01"))
		put(t, s, "grabs", "h2", grab("East of Eden S01E01 MULTI 1080p WEB X264-HiggsBoson", "S01E01"))
		return s
	}

	t.Run("backfill alone cannot choose", func(t *testing.T) {
		s := seed(t)
		runInTx(t, s, migrateBackfillGrabSeries)
		for _, h := range []string{"h1", "h2"} {
			if got := grabSeriesName(t, s, h); got != "" {
				t.Errorf("%s resolved to %q with both keys present", h, got)
			}
		}
	})

	t.Run("merge then backfill resolves both", func(t *testing.T) {
		s := seed(t)
		if err := s.runMigrations(migrationsUpTo(6)); err != nil {
			t.Fatalf("runMigrations: %v", err)
		}
		for _, h := range []string{"h1", "h2"} {
			if got := grabSeriesName(t, s, h); got != "east of eden 2026" {
				t.Errorf("%s series_name = %q, want %q", h, got, "east of eden 2026")
			}
		}
		// Both grabs point at the one surviving record, so the janitor can
		// un-track the episode when either torrent dies.
		if _, ok := get(t, s, "series", "east of eden|S01E01"); ok {
			t.Error("the duplicate tracker key survived the merge")
		}
	})
}
