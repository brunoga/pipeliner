package main

import (
	"flag"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func tmpConfig(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.star")
}

func openTrackerDB(t *testing.T, cfg string) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(dbPath(cfg))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCmdTrackerMarkAndForgetSeries(t *testing.T) {
	cfg := tmpConfig(t)

	if code := cmdTracker([]string{"mark-series", "--config", cfg, "Star Trek: Strange New Worlds", "s4e5", "--quality", "1080p web"}); code != 0 {
		t.Fatalf("mark-series exit %d", code)
	}
	db := openTrackerDB(t, cfg)
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if !tr.IsSeen("star trek strange new worlds", "S04E05") {
		t.Fatalf("episode not marked under normalized/canonical key")
	}
	db.Close()

	if code := cmdTracker([]string{"forget-series", "--config", cfg, "Star Trek: Strange New Worlds", "S04E05"}); code != 0 {
		t.Fatalf("forget-series exit %d", code)
	}
	db2 := openTrackerDB(t, cfg)
	tr2 := series.NewTracker(db2.Bucket(series.TrackerBucketName))
	if tr2.IsSeen("star trek strange new worlds", "S04E05") {
		t.Errorf("episode still present after forget")
	}
}

func TestCmdTrackerMarkMovie(t *testing.T) {
	cfg := tmpConfig(t)
	if code := cmdTracker([]string{"mark-movie", "--config", cfg, "--year", "2024", "Furiosa: A Mad Max Saga"}); code != 0 {
		t.Fatalf("mark-movie exit %d", code)
	}
	db := openTrackerDB(t, cfg)
	tr := movies.NewTracker(db.Bucket(movies.TrackerBucketName))
	if !tr.IsSeen("furiosa a mad max saga", 2024, false) {
		t.Errorf("movie not marked under normalized title")
	}
}

func TestCmdTrackerBadEpisodeID(t *testing.T) {
	cfg := tmpConfig(t)
	if code := cmdTracker([]string{"mark-series", "--config", cfg, "Silo", "garbage"}); code != 1 {
		t.Errorf("expected exit 1 for bad episode id, got %d", code)
	}
}

func TestCmdTrackerUnknownOpAndNoArgs(t *testing.T) {
	if code := cmdTracker(nil); code != 1 {
		t.Errorf("expected exit 1 with no args, got %d", code)
	}
	if code := cmdTracker([]string{"frobnicate"}); code != 1 {
		t.Errorf("expected exit 1 for unknown op, got %d", code)
	}
}

// TestFlagsFirst covers the argument reordering that makes flags work on
// either side of the positional arguments. Go's flag package stops at the
// first non-flag, so without this a flag written after the title is dropped
// in silence.
func TestFlagsFirst(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("config", "", "")
		fs.Int("year", 0, "")
		fs.Bool("3d", false, "")
		return fs
	}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"already first", []string{"--year", "2008", "bolt"}, []string{"--year", "2008", "bolt"}},
		{"flags after", []string{"bolt", "--year", "2008"}, []string{"--year", "2008", "bolt"}},
		{"bool consumes nothing", []string{"bolt", "--3d", "--year", "2008"},
			[]string{"--3d", "--year", "2008", "bolt"}},
		{"equals form", []string{"bolt", "--year=2008"}, []string{"--year=2008", "bolt"}},
		{"interleaved", []string{"--3d", "bolt", "--year", "2008"},
			[]string{"--3d", "--year", "2008", "bolt"}},
		{"two positionals", []string{"show", "S01E01", "--config", "c"},
			[]string{"--config", "c", "show", "S01E01"}},
		{"double dash ends flags", []string{"--3d", "--", "-weird-title"},
			[]string{"--3d", "-weird-title"}},
		{"single dash is positional", []string{"-", "--3d"}, []string{"--3d", "-"}},
	}
	for _, c := range cases {
		got := flagsFirst(newFS(), c.in)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %q, want %q", c.name, got, c.want)
				break
			}
		}
	}
}

// TestCmdTrackerFlagsAfterTitle is the regression test for the live failure:
// every flag after the title was ignored, so this reported success having
// written nothing to the configured store.
func TestCmdTrackerFlagsAfterTitle(t *testing.T) {
	cfg := tmpConfig(t)
	if code := cmdTracker([]string{"mark-movie", "Bolt", "--year", "2008", "--3d",
		"--config", cfg, "--quality", "1080p bluray"}); code != 0 {
		t.Fatalf("mark-movie exit %d", code)
	}
	db := openTrackerDB(t, cfg)
	tr := movies.NewTracker(db.Bucket(movies.TrackerBucketName))
	if !tr.IsSeen("bolt", 2008, true) {
		t.Fatal("movie not marked: flags after the title were dropped")
	}
	// The quality flag was in the dropped tail too.
	rec, ok := tr.LatestNearYear("bolt", 2008, true)
	if !ok {
		t.Fatal("no record")
	}
	if rec.Quality.String() == "" {
		t.Error("quality empty: --quality after the title was dropped")
	}
	db.Close()

	if code := cmdTracker([]string{"forget-movie", "Bolt", "--year", "2008", "--3d",
		"--config", cfg}); code != 0 {
		t.Fatalf("forget-movie exit %d", code)
	}
	db2 := openTrackerDB(t, cfg)
	tr2 := movies.NewTracker(db2.Bucket(movies.TrackerBucketName))
	if tr2.IsSeen("bolt", 2008, true) {
		t.Error("movie still tracked after forget")
	}
}

// TestCmdTrackerMovieRequiresYear guards against operating on key "<title>|0",
// which no real record can have.
func TestCmdTrackerMovieRequiresYear(t *testing.T) {
	cfg := tmpConfig(t)
	for _, op := range []string{"mark-movie", "forget-movie"} {
		if code := cmdTracker([]string{op, "--config", cfg, "Bolt"}); code != 1 {
			t.Errorf("%s without --year: exit %d, want 1", op, code)
		}
		if code := cmdTracker([]string{op, "--config", cfg, "--year", "0", "Bolt"}); code != 1 {
			t.Errorf("%s with --year 0: exit %d, want 1", op, code)
		}
		if code := cmdTracker([]string{op, "--config", cfg, "--year", "-5", "Bolt"}); code != 1 {
			t.Errorf("%s with negative year: exit %d, want 1", op, code)
		}
	}
}

// TestCmdTrackerForgetMissingFails pins that forgetting nothing is an error.
// Reporting success made a typo read exactly like a completed job.
func TestCmdTrackerForgetMissingFails(t *testing.T) {
	cfg := tmpConfig(t)
	if code := cmdTracker([]string{"forget-movie", "--config", cfg, "--year", "1999",
		"Never Tracked"}); code != 1 {
		t.Errorf("forget-movie on an untracked title: exit %d, want 1", code)
	}
	if code := cmdTracker([]string{"forget-series", "--config", cfg, "Never Tracked",
		"S01E01"}); code != 1 {
		t.Errorf("forget-series on an untracked show: exit %d, want 1", code)
	}
	// The 3D and 2D records are separate keys: forgetting one must not
	// report success for the other.
	if code := cmdTracker([]string{"mark-movie", "--config", cfg, "--year", "2008", "Bolt"}); code != 0 {
		t.Fatalf("mark-movie exit %d", code)
	}
	if code := cmdTracker([]string{"forget-movie", "--config", cfg, "--year", "2008",
		"--3d", "Bolt"}); code != 1 {
		t.Errorf("forgetting the 3D version of a 2D-only record: exit %d, want 1", code)
	}
}
