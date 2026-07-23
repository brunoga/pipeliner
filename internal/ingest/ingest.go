// Package ingest provides the in-process handoff between the web server's
// push endpoint (POST /api/ingest/{queue}) and webhook source plugins. Queues
// are bounded and in-memory: a restart drops undrained items, which is the
// honest trade-off for push sources — announcers re-announce, and the
// endpoint reports how many items were accepted.
package ingest

import "sync"

// MaxQueue bounds each named queue; pushes beyond it are dropped (reported
// to the caller) rather than growing without bound.
const MaxQueue = 1000

// Item is one pushed entry-to-be.
type Item struct {
	Title  string         `json:"title"`
	URL    string         `json:"url"`
	Fields map[string]any `json:"fields,omitempty"`
}

var (
	mu     sync.Mutex
	queues = map[string][]Item{}
)

// Enqueue appends items to the named queue (created on first use), returning
// how many were accepted and how many were dropped at the cap.
func Enqueue(queue string, items []Item) (accepted, dropped int) {
	mu.Lock()
	defer mu.Unlock()
	q := queues[queue]
	for _, it := range items {
		if len(q) >= MaxQueue {
			dropped++
			continue
		}
		q = append(q, it)
		accepted++
	}
	queues[queue] = q
	return accepted, dropped
}

// Drain removes and returns everything in the named queue.
func Drain(queue string) []Item {
	mu.Lock()
	defer mu.Unlock()
	items := queues[queue]
	delete(queues, queue)
	return items
}

// Len reports the current queue depth (tests and diagnostics).
func Len(queue string) int {
	mu.Lock()
	defer mu.Unlock()
	return len(queues[queue])
}
