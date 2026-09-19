package ingest

import (
	"fmt"
	"testing"
)

func TestEnqueueDrainCycle(t *testing.T) {
	q := "t1"
	a, d := Enqueue(q, []Item{{Title: "a"}, {Title: "b"}})
	if a != 2 || d != 0 {
		t.Fatalf("accepted=%d dropped=%d", a, d)
	}
	items := Drain(q)
	if len(items) != 2 || items[0].Title != "a" {
		t.Fatalf("drain: %+v", items)
	}
	if got := Drain(q); len(got) != 0 {
		t.Fatal("second drain must be empty")
	}
}

func TestQueueCap(t *testing.T) {
	q := "t2"
	big := make([]Item, MaxQueue+10)
	for i := range big {
		big[i] = Item{Title: fmt.Sprint(i)}
	}
	a, d := Enqueue(q, big)
	if a != MaxQueue || d != 10 {
		t.Fatalf("accepted=%d dropped=%d", a, d)
	}
	if Len(q) != MaxQueue {
		t.Fatalf("len=%d", Len(q))
	}
	Drain(q)
}

func TestPeekLeavesQueueIntact(t *testing.T) {
	q := "peek-test"
	Enqueue(q, []Item{{Title: "a"}, {Title: "b"}})
	got := Peek(q)
	if len(got) != 2 {
		t.Fatalf("peek: got %d items, want 2", len(got))
	}
	if Len(q) != 2 {
		t.Errorf("peek must not consume: depth %d, want 2", Len(q))
	}
	// Mutating the returned copy must not corrupt the queue.
	got[0].Title = "mutated"
	if drained := Drain(q); drained[0].Title != "a" {
		t.Errorf("peek must return a copy, queue holds %q", drained[0].Title)
	}
}
