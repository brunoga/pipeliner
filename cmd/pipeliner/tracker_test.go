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

func TestMovieTrackerBucket(t *testing.T) {
	if got := movieTrackerBucket(""); got != movies.TrackerBucketName {
		t.Errorf("empty pipeline = %q, want %q", got, movies.TrackerBucketName)
	}
	if got, want := movieTrackerBucket("3d-mvc-harvest"), "movies:3d-mvc-harvest"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCmdTrackerPerPipeline covers the gap that --pipeline closes: a movies
// node with local=true tracks into movies:<task>, which the CLI could not
// reach at all. The two trackers must stay independent in both directions.
func TestCmdTrackerPerPipeline(t *testing.T) {
	cfg := tmpConfig(t)
	const pipe = "3d-mvc-harvest"

	if code := cmdTracker([]string{"mark-movie", "--config", cfg, "--year", "2001",
		"--3d", "--pipeline", pipe, "Shrek"}); code != 0 {
		t.Fatalf("mark-movie --pipeline exit %d", code)
	}
	db := openTrackerDB(t, cfg)
	local := movies.NewTracker(db.Bucket("movies:" + pipe))
	shared := movies.NewTracker(db.Bucket(movies.TrackerBucketName))
	if !local.IsSeen("shrek", 2001, true) {
		t.Error("not marked in the per-pipeline bucket")
	}
	if shared.IsSeen("shrek", 2001, true) {
		t.Error("marking a pipeline tracker leaked into the shared one")
	}
	db.Close()

	// Forgetting from the shared tracker must not touch the pipeline's.
	if code := cmdTracker([]string{"forget-movie", "--config", cfg, "--year", "2001",
		"--3d", "Shrek"}); code != 1 {
		t.Error("forget from the shared tracker should fail: the record is not there")
	}
	db2 := openTrackerDB(t, cfg)
	if !movies.NewTracker(db2.Bucket("movies:"+pipe)).IsSeen("shrek", 2001, true) {
		t.Error("a failed shared-tracker forget removed the pipeline's record")
	}
	db2.Close()

	if code := cmdTracker([]string{"forget-movie", "--config", cfg, "--year", "2001",
		"--3d", "--pipeline", pipe, "Shrek"}); code != 0 {
		t.Fatalf("forget-movie --pipeline exit %d", code)
	}
	db3 := openTrackerDB(t, cfg)
	if movies.NewTracker(db3.Bucket("movies:"+pipe)).IsSeen("shrek", 2001, true) {
		t.Error("still tracked in the per-pipeline bucket after forget")
	}
}

// TestOtherMovieBuckets pins the hint that turns "no such record" into the
// next command to run.
func TestOtherMovieBuckets(t *testing.T) {
	cfg := tmpConfig(t)
	if code := cmdTracker([]string{"mark-movie", "--config", cfg, "--year", "2001",
		"--3d", "--pipeline", "3d-mvc-harvest", "Shrek"}); code != 0 {
		t.Fatalf("mark exit %d", code)
	}
	if code := cmdTracker([]string{"mark-movie", "--config", cfg, "--year", "2001",
		"--3d", "--pipeline", "3d-mvc-ondemand", "Shrek"}); code != 0 {
		t.Fatalf("mark exit %d", code)
	}
	db := openTrackerDB(t, cfg)
	defer db.Close()

	key := movies.RecordKey("shrek", 2001, true)
	got := otherMovieBuckets(db, movies.TrackerBucketName, key)
	want := []string{"movies:3d-mvc-harvest", "movies:3d-mvc-ondemand"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	// The excluded bucket is never reported back to the caller.
	if g := otherMovieBuckets(db, "movies:3d-mvc-harvest", key); len(g) != 1 ||
		g[0] != "movies:3d-mvc-ondemand" {
		t.Errorf("exclude not honoured: %q", g)
	}
	// A key nobody tracks yields nothing.
	if g := otherMovieBuckets(db, movies.TrackerBucketName,
		movies.RecordKey("nope", 1999, false)); len(g) != 0 {
		t.Errorf("unexpected buckets for an untracked key: %q", g)
	}
}
