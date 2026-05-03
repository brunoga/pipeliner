package series

import (
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/store"
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

// --- SQLiteStore (dedicated table) ---

func openSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ss, err := NewSQLiteStore(s.DB())
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

func TestSQLiteStoreIsSeenMark(t *testing.T) {
	ss := openSQLiteStore(t)

	if ss.IsSeen("My Show", "S01E01") {
		t.Fatal("should not be seen before Mark")
	}

	err := ss.Mark(Record{
		SeriesName:   "My Show",
		EpisodeID:    "S01E01",
		DownloadedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Mark: %v", err)
	}

	if !ss.IsSeen("My Show", "S01E01") {
		t.Error("should be seen after Mark")
	}
	if ss.IsSeen("My Show", "S01E02") {
		t.Error("S01E02 should not be seen")
	}
}

func TestSQLiteStoreLatest(t *testing.T) {
	ss := openSQLiteStore(t)

	now := time.Now()
	for _, r := range []Record{
		{SeriesName: "My Show", EpisodeID: "S01E01", DownloadedAt: now.Add(-2 * time.Hour)},
		{SeriesName: "My Show", EpisodeID: "S01E03", DownloadedAt: now},
		{SeriesName: "My Show", EpisodeID: "S01E02", DownloadedAt: now.Add(-time.Hour)},
	} {
		if err := ss.Mark(r); err != nil {
			t.Fatal(err)
		}
	}

	latest, ok := ss.Latest("My Show")
	if !ok {
		t.Fatal("expected a latest record")
	}
	if latest.EpisodeID != "S01E03" {
		t.Errorf("want S01E03 as latest, got %q", latest.EpisodeID)
	}
}

func TestSQLiteStoreLatestEmpty(t *testing.T) {
	ss := openSQLiteStore(t)
	_, ok := ss.Latest("Unknown Show")
	if ok {
		t.Error("expected no latest for unseen series")
	}
}

func TestSQLiteStoreLatestIsolatedBySeries(t *testing.T) {
	ss := openSQLiteStore(t)
	for _, r := range []Record{
		{SeriesName: "Show A", EpisodeID: "S01E01", DownloadedAt: time.Now()},
		{SeriesName: "Show B", EpisodeID: "S02E05", DownloadedAt: time.Now()},
	} {
		if err := ss.Mark(r); err != nil {
			t.Fatal(err)
		}
	}

	latA, okA := ss.Latest("Show A")
	latB, okB := ss.Latest("Show B")
	if !okA || latA.EpisodeID != "S01E01" {
		t.Errorf("Show A latest: %v %v", okA, latA)
	}
	if !okB || latB.EpisodeID != "S02E05" {
		t.Errorf("Show B latest: %v %v", okB, latB)
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
