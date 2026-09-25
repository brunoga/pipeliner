// Package settle implements the settle window shared by the movies and
// series filters: the delay between first seeing a download-worthy release
// for an item and actually grabbing one.
//
// Releases arrive in waves — 1080p, then 2160p, then an HDR pass, then an
// Atmos remux, often within hours — and grabbing on sight downloads every
// rung of that ladder. Delaying each *release* by a fixed age does not help:
// it shifts every rung later by the same amount. The window is therefore
// anchored to the item.
//
// Waiting alone is not enough, because source feeds are shallow: indexers
// commonly return only their newest ~50 items, a few hours' worth, so by the
// time a long window elapses the whole wave may have scrolled out of the feed
// and there would be nothing left to accept. The tracker therefore records
// the best candidate seen so far, and the caller downloads that remembered
// release when the window expires — whether or not it is still being
// advertised.
package settle

import (
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
)

// bucket is the minimal key-value interface the tracker requires.
type bucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
	Delete(key string) error
	Keys() ([]string, error)
}

// Candidate is a release being offered to a settle window.
type Candidate struct {
	URL     string          `json:"url"`
	Title   string          `json:"title"`
	Quality quality.Quality `json:"quality"`
	// Fields is the entry's field map, kept so the winner can be rebuilt and
	// downloaded after it has left the feed.
	Fields map[string]any `json:"fields,omitempty"`
}

// Record is the stored state of one item's settle window.
type Record struct {
	FirstSeen time.Time `json:"first_seen"`
	Best      Candidate `json:"best"`
}

// Tracker persists settle windows in a bucket.
type Tracker struct{ b bucket }

// New returns a tracker backed by the given bucket.
func New(b bucket) *Tracker { return &Tracker{b: b} }

// Offer registers a download-worthy candidate for key and reports how much of
// the settle window remains. A non-positive result means the window has
// elapsed and the caller should download now. The best candidate seen so far
// is retained, so the winner survives even after the feed forgets it.
func (t *Tracker) Offer(key string, c Candidate, window time.Duration, now time.Time) time.Duration {
	if t == nil || t.b == nil || window <= 0 {
		return 0
	}
	var rec Record
	found, err := t.b.Get(key, &rec)
	if err != nil || !found || rec.FirstSeen.IsZero() {
		_ = t.b.Put(key, Record{FirstSeen: now, Best: c})
		return window
	}
	// Keep only the best candidate: every later offer is compared against the
	// incumbent, so one record always holds the highest quality of the wave.
	if c.Quality.Better(rec.Best.Quality) || rec.Best.URL == "" {
		rec.Best = c
		_ = t.b.Put(key, rec)
	}
	if elapsed := now.Sub(rec.FirstSeen); elapsed < window {
		return window - elapsed
	}
	return 0
}

// Best returns the best candidate recorded for key.
func (t *Tracker) Best(key string) (Candidate, bool) {
	if t == nil || t.b == nil {
		return Candidate{}, false
	}
	var rec Record
	found, err := t.b.Get(key, &rec)
	if err != nil || !found || rec.Best.URL == "" {
		return Candidate{}, false
	}
	return rec.Best, true
}

// KeyedCandidate pairs a stored key with its winning release.
type KeyedCandidate struct {
	Key  string
	Best Candidate
}

// Expired lists items whose settle window has elapsed and that still hold a
// recorded winner, restricted to the keys owned by task. Callers use it to
// download a winner that is no longer advertised by any source.
//
// The task restriction is load-bearing, not hygiene: a winner is revived by
// injecting it straight into the caller's filter, skipping every upstream
// node, so returning another pipeline's pending release would smuggle it
// past gates it never passed.
func (t *Tracker) Expired(task string, window time.Duration, now time.Time) []KeyedCandidate {
	if t == nil || t.b == nil || window <= 0 || task == "" {
		return nil
	}
	keys, err := t.b.Keys()
	if err != nil {
		return nil
	}
	prefix := TaskPrefix(task)
	var out []KeyedCandidate
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		var rec Record
		found, err := t.b.Get(k, &rec)
		if err != nil || !found || rec.FirstSeen.IsZero() || rec.Best.URL == "" {
			continue
		}
		if now.Sub(rec.FirstSeen) >= window {
			out = append(out, KeyedCandidate{Key: k, Best: rec.Best})
		}
	}
	return out
}

// Clear ends the window for key, so the next wave starts a fresh timer.
func (t *Tracker) Clear(key string) {
	if t == nil || t.b == nil {
		return
	}
	_ = t.b.Delete(key)
}
