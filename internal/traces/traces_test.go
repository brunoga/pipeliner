package traces

import (
	"fmt"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/store"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db.Bucket(BucketName))
}

func rt(task, id string) RunTrace {
	return RunTrace{RunID: id, Task: task, At: time.Now(),
		Entries: []executor.EntryTrace{{Title: "x", URL: "u", Final: "accepted"}}}
}

func TestPutGetList(t *testing.T) {
	s := newStore(t)
	if err := s.Put(rt("tv", "r1")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("tv", "r1")
	if err != nil || got.RunID != "r1" || len(got.Entries) != 1 {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	metas, err := s.List("tv")
	if err != nil || len(metas) != 1 || metas[0].Entries != 1 {
		t.Fatalf("list: %+v err=%v", metas, err)
	}
	if _, err := s.Get("tv", "nope"); err == nil {
		t.Fatal("missing run must error")
	}
	if metas, _ := s.List("other"); len(metas) != 0 {
		t.Fatal("unknown task must list empty")
	}
}

func TestCapEvictsOldest(t *testing.T) {
	s := newStore(t)
	for i := 0; i < maxRunsPerTask+5; i++ {
		if err := s.Put(rt("tv", fmt.Sprintf("r%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	metas, _ := s.List("tv")
	if len(metas) != maxRunsPerTask {
		t.Fatalf("want %d kept, got %d", maxRunsPerTask, len(metas))
	}
	if metas[0].RunID != "r05" {
		t.Errorf("oldest kept should be r05, got %s", metas[0].RunID)
	}
	if _, err := s.Get("tv", "r00"); err == nil {
		t.Error("evicted run must be deleted")
	}
	if _, err := s.Get("tv", "r24"); err != nil {
		t.Errorf("newest run must exist: %v", err)
	}
}

func rtWith(task, id string, at time.Time, entries ...executor.EntryTrace) RunTrace {
	return RunTrace{RunID: id, Task: task, At: at, Entries: entries}
}

func TestSearchAcrossRunsAndTasks(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	// Two runs of "tv" that both saw SNW, plus a "movies" run that didn't.
	if err := s.Put(rtWith("tv", "r1", base,
		executor.EntryTrace{Title: "Star Trek Strange New Worlds S04E05 720p", URL: "u1", Final: "rejected", Reason: "dedup: better copy"},
		executor.EntryTrace{Title: "Silo S01E01", URL: "u2", Final: "accepted"},
	)); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(rtWith("tv", "r2", base.Add(time.Hour),
		executor.EntryTrace{Title: "Star Trek Strange New Worlds S04E05 1080p", URL: "u3", Final: "accepted"},
	)); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(rtWith("movies", "m1", base.Add(2*time.Hour),
		executor.EntryTrace{Title: "Furiosa 2024", URL: "u4", Final: "accepted"},
	)); err != nil {
		t.Fatal(err)
	}

	occ, err := s.Search("strange new worlds", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 2 {
		t.Fatalf("expected 2 occurrences, got %d", len(occ))
	}
	// Newest first: r2 (base+1h) before r1 (base).
	if occ[0].RunID != "r2" || occ[1].RunID != "r1" {
		t.Errorf("wrong order: %s then %s", occ[0].RunID, occ[1].RunID)
	}
	if occ[0].Entry.Final != "accepted" {
		t.Errorf("occ[0] final = %q", occ[0].Entry.Final)
	}
	if occ[1].Entry.Reason != "dedup: better copy" {
		t.Errorf("occ[1] reason = %q", occ[1].Entry.Reason)
	}
}

func TestSearchCaseInsensitiveAndLimit(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	entries := []executor.EntryTrace{}
	for i := 0; i < 5; i++ {
		entries = append(entries, executor.EntryTrace{Title: fmt.Sprintf("The Show S01E%02d", i), Final: "accepted"})
	}
	if err := s.Put(rtWith("tv", "r1", now, entries...)); err != nil {
		t.Fatal(err)
	}
	occ, err := s.Search("the show", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 3 {
		t.Errorf("limit not applied: got %d", len(occ))
	}
}

func TestSearchBlankQuery(t *testing.T) {
	s := newStore(t)
	if err := s.Put(rt("tv", "r1")); err != nil {
		t.Fatal(err)
	}
	occ, err := s.Search("   ", 0)
	if err != nil || occ != nil {
		t.Errorf("blank query should match nothing, got %v err=%v", occ, err)
	}
}
