package watchdog

import (
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/traces"
)

func favs(titles ...string) []match.TitleEntry {
	out := make([]match.TitleEntry, len(titles))
	for i, t := range titles {
		out[i] = match.NewTitleEntry(t, 0)
	}
	return out
}

func occ(task, runID string, at time.Time, title, final, reason string) traces.Occurrence {
	return traces.Occurrence{Task: task, RunID: runID, At: at,
		Entry: executor.EntryTrace{Title: title, Final: final, Reason: reason}}
}

func TestDetectStuckFavoriteAcrossRuns(t *testing.T) {
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	favorites := favs("Silo", "Star Trek: Strange New Worlds", "The Ark")
	// SNW candidates appear in 3 runs, always rejected "not in list".
	var os []traces.Occurrence
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(i) * time.Hour)
		os = append(os, occ("tv", "r"+string(rune('1'+i)), at,
			"Star Trek Strange New Worlds S04E0"+string(rune('5'+i))+" 1080p WEB",
			"rejected", "series: show not in list"))
	}
	got := Detect(os, favorites, 3, 6)
	if len(got) != 1 {
		t.Fatalf("expected 1 stuck favorite, got %d: %+v", len(got), got)
	}
	if got[0].Favorite != "star trek strange new worlds" {
		t.Errorf("favorite = %q", got[0].Favorite)
	}
	if got[0].Runs != 3 {
		t.Errorf("runs = %d, want 3", got[0].Runs)
	}
	if got[0].LastReason != "series: show not in list" {
		t.Errorf("last reason = %q", got[0].LastReason)
	}
}

func TestDetectExcludesAccepted(t *testing.T) {
	base := time.Now()
	favorites := favs("Silo")
	os := []traces.Occurrence{
		occ("tv", "r1", base, "Silo S01E01 1080p", "rejected", "quality"),
		occ("tv", "r2", base, "Silo S01E01 1080p", "rejected", "quality"),
		occ("tv", "r3", base, "Silo S01E02 1080p", "accepted", ""),
	}
	if got := Detect(os, favorites, 3, 6); len(got) != 0 {
		t.Errorf("a favorite that ever accepts is not stuck, got %+v", got)
	}
}

func TestDetectExcludesFeedNoise(t *testing.T) {
	base := time.Now()
	favorites := favs("Silo")
	// Unrelated release, far from any favorite, rejected every run.
	os := []traces.Occurrence{
		occ("tv", "r1", base, "Some Completely Unrelated Show S01E01", "rejected", "not in list"),
		occ("tv", "r2", base, "Some Completely Unrelated Show S01E02", "rejected", "not in list"),
		occ("tv", "r3", base, "Some Completely Unrelated Show S01E03", "rejected", "not in list"),
	}
	if got := Detect(os, favorites, 3, 6); len(got) != 0 {
		t.Errorf("feed noise far from favorites should be excluded, got %+v", got)
	}
}

func TestDetectRespectsMinRuns(t *testing.T) {
	base := time.Now()
	favorites := favs("Silo")
	os := []traces.Occurrence{
		occ("tv", "r1", base, "Silo S01E01", "rejected", "quality"),
		occ("tv", "r2", base, "Silo S01E01", "rejected", "quality"),
	}
	if got := Detect(os, favorites, 3, 6); len(got) != 0 {
		t.Errorf("2 runs < minRuns 3 should not be stuck, got %+v", got)
	}
	if got := Detect(os, favorites, 2, 6); len(got) != 1 {
		t.Errorf("2 runs == minRuns 2 should be stuck, got %+v", got)
	}
}

func TestDetectDistanceZeroForExactMatch(t *testing.T) {
	base := time.Now()
	favorites := favs("Silo")
	os := []traces.Occurrence{
		occ("tv", "r1", base, "Silo S01E01 480p", "rejected", "quality too low"),
		occ("tv", "r2", base, "Silo S01E01 480p", "rejected", "quality too low"),
		occ("tv", "r3", base, "Silo S01E01 480p", "rejected", "quality too low"),
	}
	got := Detect(os, favorites, 3, 6)
	if len(got) != 1 || got[0].NearestDistance != 0 {
		t.Fatalf("exact-match favorite should have distance 0: %+v", got)
	}
}

func TestDetectNoFavorites(t *testing.T) {
	if got := Detect([]traces.Occurrence{occ("t", "r", time.Now(), "x", "rejected", "y")}, nil, 3, 6); got != nil {
		t.Errorf("no favorites should yield no results, got %+v", got)
	}
}
