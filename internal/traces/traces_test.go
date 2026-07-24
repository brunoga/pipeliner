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
