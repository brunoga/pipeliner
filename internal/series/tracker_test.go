package series

import (
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/untrack"
	"github.com/brunoga/pipeliner/quality"
)

// --- Tracker (bucket-backed) ---

func openTracker(t *testing.T) *Tracker {
	t.Helper()
	s, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return NewTracker(s.Bucket("series"))
}

func TestTrackerIsSeenMarkForget(t *testing.T) {
	tr := openTracker(t)

	if tr.IsSeen("My Show", "S01E01") {
		t.Fatal("should not be seen before Mark")
	}

	err := tr.Mark(Record{
		SeriesName:   "My Show",
		EpisodeID:    "S01E01",
		DownloadedAt: time.Now(),
		Quality:      quality.Quality{},
	})
	if err != nil {
		t.Fatalf("Mark: %v", err)
	}

	if !tr.IsSeen("My Show", "S01E01") {
		t.Error("should be seen after Mark")
	}
	if tr.IsSeen("My Show", "S01E02") {
		t.Error("S01E02 should not be seen")
	}

	if err := tr.Forget("My Show", "S01E01"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if tr.IsSeen("My Show", "S01E01") {
		t.Error("should not be seen after Forget")
	}
	// Forgetting an unknown episode is a no-op, not an error.
	if err := tr.Forget("My Show", "S09E09"); err != nil {
		t.Errorf("Forget unknown: %v", err)
	}
}

func TestTrackerForgetDoubleEpisodeParts(t *testing.T) {
	tr := openTracker(t)

	rec := Record{
		SeriesName:   "my show",
		EpisodeID:    "S01E01E02",
		DownloadedAt: time.Now(),
	}
	ep := &Episode{Season: 1, Episode: 1, DoubleEpisode: 2}
	if err := tr.MarkWithParts(rec, ep); err != nil {
		t.Fatalf("MarkWithParts: %v", err)
	}
	for _, id := range []string{"S01E01E02", "S01E01", "S01E02"} {
		if !tr.IsSeen("my show", id) {
			t.Fatalf("%s should be seen after MarkWithParts", id)
		}
	}

	if err := tr.Forget("my show", "S01E01E02"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	for _, id := range []string{"S01E01E02", "S01E01", "S01E02"} {
		if tr.IsSeen("my show", id) {
			t.Errorf("%s should be forgotten", id)
		}
	}
}

func TestTrackerGet(t *testing.T) {
	tr := openTracker(t)

	_, ok := tr.Get("My Show", "S01E01")
	if ok {
		t.Fatal("Get should return false before Mark")
	}

	want := quality.Quality{Resolution: 5, Source: 5}
	if err := tr.Mark(Record{
		SeriesName:   "My Show",
		EpisodeID:    "S01E01",
		DownloadedAt: time.Now(),
		Quality:      want,
	}); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	rec, ok := tr.Get("My Show", "S01E01")
	if !ok {
		t.Fatal("Get should return true after Mark")
	}
	if rec.Quality != want {
		t.Errorf("Get quality = %+v, want %+v", rec.Quality, want)
	}

	_, ok = tr.Get("My Show", "S01E02")
	if ok {
		t.Error("Get should return false for unseen episode")
	}
}

func TestTrackerLatest(t *testing.T) {
	tr := openTracker(t)

	now := time.Now()
	for _, r := range []Record{
		{SeriesName: "My Show", EpisodeID: "S01E01", DownloadedAt: now.Add(-2 * time.Hour)},
		{SeriesName: "My Show", EpisodeID: "S01E03", DownloadedAt: now},
		{SeriesName: "My Show", EpisodeID: "S01E02", DownloadedAt: now.Add(-time.Hour)},
	} {
		if err := tr.Mark(r); err != nil {
			t.Fatal(err)
		}
	}

	latest, ok := tr.Latest("My Show")
	if !ok {
		t.Fatal("expected a latest record")
	}
	if latest.EpisodeID != "S01E03" {
		t.Errorf("want S01E03 as latest, got %q", latest.EpisodeID)
	}
}

func TestTrackerLatestEmpty(t *testing.T) {
	tr := openTracker(t)
	_, ok := tr.Latest("Unknown Show")
	if ok {
		t.Error("expected no latest for unseen series")
	}
}

func TestTrackerLatestIsolatedBySeries(t *testing.T) {
	tr := openTracker(t)
	for _, r := range []Record{
		{SeriesName: "Show A", EpisodeID: "S01E01", DownloadedAt: time.Now()},
		{SeriesName: "Show B", EpisodeID: "S02E05", DownloadedAt: time.Now()},
	} {
		if err := tr.Mark(r); err != nil {
			t.Fatal(err)
		}
	}

	latA, okA := tr.Latest("Show A")
	latB, okB := tr.Latest("Show B")
	if !okA || latA.EpisodeID != "S01E01" {
		t.Errorf("Show A latest: %v %v", okA, latA)
	}
	if !okB || latB.EpisodeID != "S02E05" {
		t.Errorf("Show B latest: %v %v", okB, latB)
	}
}

func TestTrackerHighestEpisode(t *testing.T) {
	tr := openTracker(t)

	for _, r := range []Record{
		{SeriesName: "My Show", EpisodeID: "S01E01", DownloadedAt: time.Now()},
		{SeriesName: "My Show", EpisodeID: "S05E08", DownloadedAt: time.Time{}},
		{SeriesName: "My Show", EpisodeID: "S03E04", DownloadedAt: time.Now()},
	} {
		if err := tr.Mark(r); err != nil {
			t.Fatal(err)
		}
	}

	highest, ok := tr.HighestEpisode("My Show")
	if !ok {
		t.Fatal("expected a highest episode")
	}
	if highest.EpisodeID != "S05E08" {
		t.Errorf("want S05E08 as highest, got %q", highest.EpisodeID)
	}
}

func TestTrackerHighestEpisodeEmpty(t *testing.T) {
	tr := openTracker(t)
	_, ok := tr.HighestEpisode("Unknown Show")
	if ok {
		t.Error("expected no highest episode for unseen series")
	}
}

// --- MarkWithParts ---

func TestMarkWithPartsDouble(t *testing.T) {
	tr := openTracker(t)
	ep := &Episode{Season: 1, Episode: 1, DoubleEpisode: 2}
	rec := Record{SeriesName: "My Show", EpisodeID: EpisodeID(ep), DownloadedAt: time.Now()}
	if err := tr.MarkWithParts(rec, ep); err != nil {
		t.Fatalf("MarkWithParts: %v", err)
	}
	for _, id := range []string{"S01E01E02", "S01E01", "S01E02"} {
		if !tr.IsSeen("My Show", id) {
			t.Errorf("expected %s to be marked after MarkWithParts of S01E01E02", id)
		}
	}
}

func TestMarkWithPartsSingle(t *testing.T) {
	tr := openTracker(t)
	ep := &Episode{Season: 1, Episode: 5}
	rec := Record{SeriesName: "My Show", EpisodeID: EpisodeID(ep), DownloadedAt: time.Now()}
	if err := tr.MarkWithParts(rec, ep); err != nil {
		t.Fatalf("MarkWithParts: %v", err)
	}
	if !tr.IsSeen("My Show", "S01E05") {
		t.Error("expected S01E05 to be marked")
	}
}

// --- EpisodeID ---

func TestEpisodeID(t *testing.T) {
	cases := []struct {
		ep   *Episode
		want string
	}{
		{&Episode{Season: 1, Episode: 1}, "S01E01"},
		{&Episode{Season: 3, Episode: 12}, "S03E12"},
		{&Episode{Season: 1, Episode: 1, DoubleEpisode: 2}, "S01E01E02"},
		{&Episode{IsDate: true, Year: 2023, Month: 11, Day: 15}, "2023-11-15"},
		{&Episode{Episode: 123}, "EP123"},
	}
	for _, tc := range cases {
		got := EpisodeID(tc.ep)
		if got != tc.want {
			t.Errorf("EpisodeID(%+v) = %q, want %q", tc.ep, got, tc.want)
		}
	}
}

// TestMarkKeepsPreviousDownload covers the one-level undo Mark maintains, so a
// failed grab can be rolled back instead of erasing the episode's history.
func TestMarkKeepsPreviousDownload(t *testing.T) {
	tr := openTracker(t)
	first := quality.Parse("720p WEB-DL x264")
	second := quality.Parse("1080p BluRay x265")

	if err := tr.Mark(Record{SeriesName: "severance", EpisodeID: "S02E10", Quality: first}); err != nil {
		t.Fatal(err)
	}
	rec, ok := tr.Get("severance", "S02E10")
	if !ok {
		t.Fatal("first download should be tracked")
	}
	if rec.Prev != nil {
		t.Errorf("a first download has nothing to roll back to, got %+v", rec.Prev)
	}

	if err := tr.Mark(Record{SeriesName: "severance", EpisodeID: "S02E10", Quality: second}); err != nil {
		t.Fatal(err)
	}
	rec, _ = tr.Get("severance", "S02E10")
	if rec.Quality != second {
		t.Errorf("current quality = %s, want %s", rec.Quality, second)
	}
	if rec.Prev == nil || rec.Prev.Quality != first {
		t.Fatalf("Prev should hold the superseded download, got %+v", rec.Prev)
	}
}

func TestUntrackGrab(t *testing.T) {
	low := quality.Parse("720p WEB-DL x264")
	high := quality.Parse("1080p BluRay x265")

	t.Run("no record is a no-op", func(t *testing.T) {
		tr := openTracker(t)
		got, err := tr.UntrackGrab("severance", "S02E10", low, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != untrack.NoRecord {
			t.Errorf("outcome = %v, want NoRecord", got)
		}
	})

	t.Run("only download on record is deleted", func(t *testing.T) {
		tr := openTracker(t)
		if err := tr.Mark(Record{SeriesName: "severance", EpisodeID: "S02E10", Quality: low}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.UntrackGrab("severance", "S02E10", low, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != untrack.Deleted {
			t.Errorf("outcome = %v, want Deleted", got)
		}
		if tr.IsSeen("severance", "S02E10") {
			t.Error("record should be gone so another release can be tried")
		}
	})

	// The regression this exists for: a failed upgrade used to delete the
	// record outright, forgetting the copy already in the library and
	// re-downloading the episode, often worse than what it had.
	t.Run("failed upgrade rolls back to the previous download", func(t *testing.T) {
		tr := openTracker(t)
		if err := tr.Mark(Record{SeriesName: "severance", EpisodeID: "S02E10", DisplayName: "Severance", Quality: low}); err != nil {
			t.Fatal(err)
		}
		if err := tr.Mark(Record{SeriesName: "severance", EpisodeID: "S02E10", DisplayName: "Severance", Quality: high, Repack: true}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.UntrackGrab("severance", "S02E10", high, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != untrack.Restored {
			t.Fatalf("outcome = %v, want Restored", got)
		}
		rec, ok := tr.Get("severance", "S02E10")
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
		tr := openTracker(t)
		if err := tr.Mark(Record{SeriesName: "severance", EpisodeID: "S02E10", Quality: high}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.UntrackGrab("severance", "S02E10", low, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != untrack.Stale {
			t.Fatalf("outcome = %v, want Stale", got)
		}
		if rec, _ := tr.Get("severance", "S02E10"); rec.Quality != high {
			t.Errorf("quality = %s, want the later download %s", rec.Quality, high)
		}
	})

	// MarkWithParts writes the combined record plus one per part, so a
	// rollback has to move all three together or the episode ends up
	// half-tracked — the part records would still claim a download that was
	// rolled back.
	t.Run("double episode parts move with the combined record", func(t *testing.T) {
		tr := openTracker(t)
		ep := &Episode{Season: 1, Episode: 1, DoubleEpisode: 2}
		base := Record{SeriesName: "show", EpisodeID: "S01E01E02"}
		first, second := base, base
		first.Quality, second.Quality = low, high
		if err := tr.MarkWithParts(first, ep); err != nil {
			t.Fatal(err)
		}
		if err := tr.MarkWithParts(second, ep); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"S01E01E02", "S01E01", "S01E02"} {
			if rec, ok := tr.Get("show", id); !ok || rec.Quality != high {
				t.Fatalf("%s should hold the upgrade, got %+v", id, rec)
			}
		}

		got, err := tr.UntrackGrab("show", "S01E01E02", high, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != untrack.Restored {
			t.Fatalf("outcome = %v, want Restored", got)
		}
		for _, id := range []string{"S01E01E02", "S01E01", "S01E02"} {
			rec, ok := tr.Get("show", id)
			if !ok {
				t.Errorf("%s must stay tracked at the earlier quality", id)
				continue
			}
			if rec.Quality != low {
				t.Errorf("%s quality = %s, want %s", id, rec.Quality, low)
			}
		}
	})

	t.Run("double episode first grab deletes every part", func(t *testing.T) {
		tr := openTracker(t)
		ep := &Episode{Season: 1, Episode: 1, DoubleEpisode: 2}
		if err := tr.MarkWithParts(Record{SeriesName: "show", EpisodeID: "S01E01E02", Quality: low}, ep); err != nil {
			t.Fatal(err)
		}
		got, err := tr.UntrackGrab("show", "S01E01E02", low, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != untrack.Deleted {
			t.Fatalf("outcome = %v, want Deleted", got)
		}
		for _, id := range []string{"S01E01E02", "S01E01", "S01E02"} {
			if tr.IsSeen("show", id) {
				t.Errorf("%s should be gone", id)
			}
		}
	})

	t.Run("grab record without quality still rolls back", func(t *testing.T) {
		tr := openTracker(t)
		if err := tr.Mark(Record{SeriesName: "severance", EpisodeID: "S02E10", Quality: low}); err != nil {
			t.Fatal(err)
		}
		if err := tr.Mark(Record{SeriesName: "severance", EpisodeID: "S02E10", Quality: high}); err != nil {
			t.Fatal(err)
		}
		got, err := tr.UntrackGrab("severance", "S02E10", quality.Quality{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if got != untrack.Restored {
			t.Fatalf("outcome = %v, want Restored", got)
		}
		if rec, _ := tr.Get("severance", "S02E10"); rec.Quality != low {
			t.Errorf("restored quality = %s, want %s", rec.Quality, low)
		}
	})
}
