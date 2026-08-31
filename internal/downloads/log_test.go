package downloads

import (
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/store"
)

func newLog(t *testing.T) *Log {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db.Bucket(BucketName))
}

func seriesEvent(name, ep string, at time.Time, q string) Event {
	return Event{MediaType: "series", Name: name, DisplayName: name, EpisodeID: ep,
		Quality: quality.Parse(q), DownloadedAt: at, Task: "tv"}
}

func TestAppendAndQuery(t *testing.T) {
	l := newLog(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Same episode downloaded twice (720p then 1080p upgrade).
	if err := l.Append(seriesEvent("star trek strange new worlds", "S04E05", base, "720p web")); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(seriesEvent("star trek strange new worlds", "S04E05", base.Add(time.Hour), "1080p web")); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(seriesEvent("silo", "S01E01", base, "1080p web")); err != nil {
		t.Fatal(err)
	}

	got, err := l.Query("strange new worlds")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	// Newest first.
	if !got[0].DownloadedAt.After(got[1].DownloadedAt) {
		t.Errorf("events not newest-first")
	}
}

func TestQueryBlank(t *testing.T) {
	l := newLog(t)
	l.Append(seriesEvent("silo", "S01E01", time.Now(), "1080p"))
	got, err := l.Query("  ")
	if err != nil || got != nil {
		t.Errorf("blank query should match nothing, got %v", got)
	}
}

func TestAppendPreservesMultipleSameNanoUnlikelyButDistinctTimes(t *testing.T) {
	l := newLog(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := l.Append(seriesEvent("show", "S01E01", base.Add(time.Duration(i)*time.Second), "1080p")); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := l.Query("show")
	if len(got) != 3 {
		t.Errorf("expected 3 distinct events, got %d", len(got))
	}
}

func TestGroupByItem(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	events := []Event{
		seriesEvent("snw", "S04E05", base, "720p web"),
		seriesEvent("snw", "S04E05", base.Add(time.Hour), "1080p web"),
		seriesEvent("snw", "S04E06", base.Add(2*time.Hour), "1080p web"),
		{MediaType: "movie", Name: "furiosa", DisplayName: "Furiosa", Year: 2024,
			Quality: quality.Parse("1080p bluray"), DownloadedAt: base.Add(30 * time.Minute)},
	}
	hist := GroupByItem(events)
	if len(hist) != 3 {
		t.Fatalf("expected 3 items, got %d", len(hist))
	}
	// Ordered by most-recent download: S04E06 (base+2h) first.
	if hist[0].EpisodeID != "S04E06" {
		t.Errorf("first item = %+v, want S04E06", hist[0])
	}
	// The twice-downloaded episode has Count 2 and its downloads newest-first.
	var e05 *ItemHistory
	for i := range hist {
		if hist[i].EpisodeID == "S04E05" {
			e05 = &hist[i]
		}
	}
	if e05 == nil || e05.Count != 2 {
		t.Fatalf("S04E05 count = %v", e05)
	}
	if e05.Downloads[0].Quality.String() != "1080p WEB-DL" {
		t.Errorf("latest download quality = %q, want 1080p WEB-DL", e05.Downloads[0].Quality.String())
	}
}

func TestAppendZeroTimeDefaultsToNow(t *testing.T) {
	l := newLog(t)
	if err := l.Append(Event{MediaType: "series", Name: "x", EpisodeID: "S01E01"}); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Query("x")
	if len(got) != 1 || got[0].DownloadedAt.IsZero() {
		t.Errorf("zero DownloadedAt should default to now, got %v", got)
	}
}
