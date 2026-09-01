package watchdog

import (
	"strconv"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/traces"
)

// favs builds a favorite list tagged with both kinds, so the existing tests
// (which pass no taskKinds map) exercise Detect without kind scoping — every
// occurrence's task reads as unknown and correlates against all favorites.
func favs(titles ...string) []Favorite {
	out := make([]Favorite, len(titles))
	for i, t := range titles {
		out[i] = Favorite{Entry: match.NewTitleEntry(t, 0), Kind: allKinds}
	}
	return out
}

// favsKind builds a favorite list tagged with a single media kind.
func favsKind(kind MediaKind, titles ...string) []Favorite {
	out := make([]Favorite, len(titles))
	for i, t := range titles {
		out[i] = Favorite{Entry: match.NewTitleEntry(t, 0), Kind: kind}
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
	got := Detect(os, favorites, nil, 3, 6)
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
	if got := Detect(os, favorites, nil, 3, 6); len(got) != 0 {
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
	if got := Detect(os, favorites, nil, 3, 6); len(got) != 0 {
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
	if got := Detect(os, favorites, nil, 3, 6); len(got) != 0 {
		t.Errorf("2 runs < minRuns 3 should not be stuck, got %+v", got)
	}
	if got := Detect(os, favorites, nil, 2, 6); len(got) != 1 {
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
	got := Detect(os, favorites, nil, 3, 6)
	if len(got) != 1 || got[0].NearestDistance != 0 {
		t.Fatalf("exact-match favorite should have distance 0: %+v", got)
	}
}

func TestDetectNoFavorites(t *testing.T) {
	if got := Detect([]traces.Occurrence{occ("t", "r", time.Now(), "x", "rejected", "y")}, nil, nil, 3, 6); got != nil {
		t.Errorf("no favorites should yield no results, got %+v", got)
	}
}

// TestDetectDedupesFavorites verifies that the same favorite appearing more than
// once (as it does when the series and movies list caches are unioned) still
// yields a single stuck entry rather than being counted per duplicate.
func TestDetectDedupesFavorites(t *testing.T) {
	base := time.Now()
	// "Silo" listed three times, as if resolved from three pipelines.
	favorites := favs("Silo", "Silo", "Silo")
	os := []traces.Occurrence{
		occ("tv", "r1", base, "Silo S01E01 480p", "rejected", "quality too low"),
		occ("tv", "r2", base, "Silo S01E01 480p", "rejected", "quality too low"),
		occ("tv", "r3", base, "Silo S01E01 480p", "rejected", "quality too low"),
	}
	got := Detect(os, favorites, nil, 3, 6)
	if len(got) != 1 {
		t.Fatalf("duplicate favorites should collapse to one stuck entry, got %d: %+v", len(got), got)
	}
	if got[0].Favorite != "silo" {
		t.Errorf("favorite = %q, want silo", got[0].Favorite)
	}
}

// TestDetectScopesByMediaKind is the "lanterns" bug: a TV favorite must not pick
// up rejections from a movies pipeline that never uses the series list. With the
// movies task recorded as movies-only, the series favorite's candidates seen in
// that task are excluded, so it is not reported as stuck.
func TestDetectScopesByMediaKind(t *testing.T) {
	base := time.Now()
	favorites := favsKind(KindSeries, "Lanterns")
	taskKinds := map[string]MediaKind{"movies-3d": KindMovies, "tv": KindSeries}
	// A movies pipeline keeps rejecting a "Lanterns" 3D release (missing video_year).
	os := []traces.Occurrence{
		occ("movies-3d", "r1", base, "LANTERNS S01E02 MULTi 1080p 3D FSBS WEBrip x264", "rejected", "missing required field: video_year"),
		occ("movies-3d", "r2", base, "LANTERNS S01E02 MULTi 1080p 3D FSBS WEBrip x264", "rejected", "missing required field: video_year"),
		occ("movies-3d", "r3", base, "LANTERNS S01E02 MULTi 1080p 3D FSBS WEBrip x264", "rejected", "missing required field: video_year"),
	}
	if got := Detect(os, favorites, taskKinds, 3, 6); len(got) != 0 {
		t.Errorf("a TV favorite must not be stuck on a movies pipeline's rejections, got %+v", got)
	}

	// The same occurrences in an actual series pipeline do count.
	var tvOcc []traces.Occurrence
	for _, o := range os {
		o.Task = "tv"
		tvOcc = append(tvOcc, o)
	}
	if got := Detect(tvOcc, favorites, taskKinds, 3, 6); len(got) != 1 {
		t.Errorf("a TV favorite stuck on a series pipeline should be reported, got %+v", got)
	}
}

// TestDetectUnknownTaskCorrelatesAllKinds documents the fallback: a task absent
// from the kinds map (e.g. an older database with no persisted map) correlates
// against all favorites, preserving pre-scoping behavior.
func TestDetectUnknownTaskCorrelatesAllKinds(t *testing.T) {
	base := time.Now()
	favorites := favsKind(KindSeries, "Silo")
	os := []traces.Occurrence{
		occ("mystery", "r1", base, "Silo S01E01 480p", "rejected", "quality too low"),
		occ("mystery", "r2", base, "Silo S01E01 480p", "rejected", "quality too low"),
		occ("mystery", "r3", base, "Silo S01E01 480p", "rejected", "quality too low"),
	}
	// Empty map → "mystery" is unknown → correlates against all kinds.
	if got := Detect(os, favorites, map[string]MediaKind{}, 3, 6); len(got) != 1 {
		t.Errorf("unknown task should correlate against all favorites, got %+v", got)
	}
}

// TestDetectMemoizedMatchesUnmemoized guards the memoization/dedup rewrite: a
// dataset with recurring show names and duplicate favorites must produce the
// same result the naive per-occurrence probe would.
func TestDetectMemoizedMatchesUnmemoized(t *testing.T) {
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	favorites := favs("Silo", "Star Trek: Strange New Worlds", "The Ark", "Silo")
	var os []traces.Occurrence
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Hour)
		rid := "r" + string(rune('1'+i))
		os = append(os,
			occ("tv", rid, at, "Star Trek Strange New Worlds S04E05 1080p WEB", "rejected", "series: show not in list"),
			occ("tv", rid, at, "The Ark S02E0"+string(rune('1'+i))+" 720p", "rejected", "quality"),
		)
	}
	got := Detect(os, favorites, nil, 3, 6)
	if len(got) != 2 {
		t.Fatalf("expected 2 stuck favorites, got %d: %+v", len(got), got)
	}
	// Both favorites match exactly after normalization (the colon in SNW folds),
	// so both are seen across all 5 runs and never accepted.
	byName := map[string]StuckFavorite{}
	for _, s := range got {
		byName[s.Favorite] = s
	}
	if s, ok := byName["star trek strange new worlds"]; !ok || s.Runs != 5 {
		t.Errorf("SNW: %+v (ok=%v)", s, ok)
	}
	if s, ok := byName["the ark"]; !ok || s.Runs != 5 || s.NearestDistance != 0 {
		t.Errorf("The Ark: %+v (ok=%v)", s, ok)
	}
}

// BenchmarkDetectLargeStore approximates a production-scale trace store: many
// occurrences of recurring shows against a large favorite list. Before the
// memoization fix this was O(occ × fav × len²) and drove the web handler past
// the reverse-proxy timeout; the per-show-name cache collapses the probe work
// to O(distinct shows × fav).
func BenchmarkDetectLargeStore(b *testing.B) {
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	var favorites []Favorite
	for i := 0; i < 300; i++ {
		favorites = append(favorites, Favorite{Entry: match.NewTitleEntry("Favorite Show Number "+strconv.Itoa(i), 0), Kind: KindSeries})
	}
	favorites = append(favorites, Favorite{Entry: match.NewTitleEntry("Star Trek: Strange New Worlds", 0), Kind: KindSeries})
	// 40 distinct shows, each recurring across 20 runs with ~25 entries → 20k occ.
	var os []traces.Occurrence
	for run := 0; run < 20; run++ {
		at := base.Add(time.Duration(run) * time.Hour)
		rid := "r" + strconv.Itoa(run)
		for show := 0; show < 40; show++ {
			title := "Star Trek Strange New Worlds S04E0" + strconv.Itoa(show%9+1) + " 1080p WEB"
			if show%2 == 0 {
				title = "Random Feed Show " + strconv.Itoa(show) + " S01E0" + strconv.Itoa(run%9+1)
			}
			for dup := 0; dup < 25; dup++ {
				os = append(os, occ("tv", rid, at, title, "rejected", "series: show not in list"))
			}
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Detect(os, favorites, nil, 3, 6)
	}
}
