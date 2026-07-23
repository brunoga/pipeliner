package series

import (
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/store"
)

func openInactiveSet(t *testing.T) *InactiveSet {
	t.Helper()
	s, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return NewInactiveSet(s.Bucket(InactiveBucketName))
}

func TestInactiveSetRoundTrip(t *testing.T) {
	set := openInactiveSet(t)

	if set.IsInactive("my show") {
		t.Fatal("fresh set should have nothing inactive")
	}

	if err := set.Deactivate("my show", "complete"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	rec, ok := set.Get("my show")
	if !ok {
		t.Fatal("Get should find the deactivated show")
	}
	if rec.Reason != "complete" {
		t.Errorf("reason: got %q, want %q", rec.Reason, "complete")
	}
	if rec.DeactivatedAt.IsZero() {
		t.Error("DeactivatedAt should be set")
	}
	if !set.IsInactive("my show") {
		t.Error("IsInactive should be true after Deactivate")
	}
	if set.IsInactive("other show") {
		t.Error("other shows must not be affected")
	}

	if err := set.Reactivate("my show"); err != nil {
		t.Fatalf("Reactivate: %v", err)
	}
	if set.IsInactive("my show") {
		t.Error("IsInactive should be false after Reactivate")
	}
}

func TestInactiveSetReactivateUnknownIsNoop(t *testing.T) {
	set := openInactiveSet(t)
	if err := set.Reactivate("never seen"); err != nil {
		t.Fatalf("Reactivate on unknown show: %v", err)
	}
}

func TestInactiveSetNilSafe(t *testing.T) {
	var set *InactiveSet
	if set.IsInactive("anything") {
		t.Error("nil set must report nothing inactive")
	}
	if _, ok := set.Get("anything"); ok {
		t.Error("nil set Get must return false")
	}
}

func TestTrackerSummaries(t *testing.T) {
	tr := openTracker(t)

	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	records := []Record{
		{SeriesName: "show a", DisplayName: "Show A", EpisodeID: "S01E01", DownloadedAt: t0},
		{SeriesName: "show a", DisplayName: "Show A (2024)", EpisodeID: "S01E02", DownloadedAt: t1},
		{SeriesName: "show b", EpisodeID: "S02E10", DownloadedAt: t0},
	}
	for _, r := range records {
		if err := tr.Mark(r); err != nil {
			t.Fatalf("Mark: %v", err)
		}
	}

	sums, err := tr.Summaries()
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}
	if len(sums) != 2 {
		t.Fatalf("got %d summaries, want 2", len(sums))
	}

	a := sums[0]
	if a.Name != "show a" {
		t.Fatalf("summaries not sorted by name: first is %q", a.Name)
	}
	if a.EpisodeCount != 2 {
		t.Errorf("show a episode count: got %d, want 2", a.EpisodeCount)
	}
	if a.NewestEpisodeID != "S01E02" {
		t.Errorf("show a newest episode: got %q, want S01E02", a.NewestEpisodeID)
	}
	if !a.LastDownloadedAt.Equal(t1) {
		t.Errorf("show a last downloaded: got %v, want %v", a.LastDownloadedAt, t1)
	}
	// DisplayName comes from the most recently downloaded record.
	if a.DisplayName != "Show A (2024)" {
		t.Errorf("show a display name: got %q, want %q", a.DisplayName, "Show A (2024)")
	}

	b := sums[1]
	if b.Name != "show b" {
		t.Fatalf("second summary: got %q, want show b", b.Name)
	}
	if b.DisplayName != "" {
		t.Errorf("show b display name should be empty, got %q", b.DisplayName)
	}
	if b.EpisodeCount != 1 || b.NewestEpisodeID != "S02E10" {
		t.Errorf("show b summary: %+v", b)
	}
}
