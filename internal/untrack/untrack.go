// Package untrack records when content was last un-tracked after a failed
// grab, so the filters can hold off on re-grabbing it for a while.
//
// Without a hold, a film whose releases keep dying is retried at the pipeline's
// own cadence: the janitor fails the torrent, un-tracks the film, and the next
// scheduled run grabs the next release of the same film — which dies the same
// way. Observed in production as seven grabs of one title in seven consecutive
// hourly runs, each one downloading and then deleting its data. Alternating
// releases is the right behaviour; doing it once an hour is not.
//
// Like the grabs and seen_failed buckets this one is deliberately not
// namespaced by task: the janitor that writes the marker is a different
// pipeline from the one that reads it.
package untrack

import (
	"fmt"
	"strings"
	"time"
)

// BucketName is the store bucket holding content key → last un-track time.
const BucketName = "untrack_log"

// Record is the stored marker.
type Record struct {
	// At is when the content was last un-tracked after a failed grab.
	At time.Time `json:"at"`
	// Release and Reason describe the grab that died, for the DB browser and
	// for the reject reason the filters report.
	Release string `json:"release,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// bucket is the minimal key-value interface Store requires; store.Bucket
// satisfies it automatically.
type bucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
}

// Store persists un-track markers.
type Store struct {
	bucket bucket
}

// NewStore wraps a bucket as a Store.
func NewStore(b bucket) *Store {
	return &Store{bucket: b}
}

// Mark records that key was just un-tracked.
func (s *Store) Mark(key string, r Record) error {
	if r.At.IsZero() {
		r.At = time.Now()
	}
	return s.bucket.Put(key, r)
}

// Remaining reports how much of a cooldown window is left for key, and
// whether the hold is still active. A zero or negative window is never active,
// which is how callers disable the feature.
func (s *Store) Remaining(key string, window time.Duration, now time.Time) (time.Duration, bool) {
	if window <= 0 {
		return 0, false
	}
	var rec Record
	found, err := s.bucket.Get(key, &rec)
	if err != nil || !found || rec.At.IsZero() {
		return 0, false
	}
	left := window - now.Sub(rec.At)
	if left <= 0 {
		return 0, false
	}
	return left, true
}

// Last returns the stored marker for key.
func (s *Store) Last(key string) (Record, bool) {
	var rec Record
	found, err := s.bucket.Get(key, &rec)
	if err != nil || !found {
		return Record{}, false
	}
	return rec, true
}

// MovieKey builds the key for a movie, matching the movies tracker's own
// (title, year, 3D) identity.
func MovieKey(title string, year int, is3D bool) string {
	if is3D {
		return fmt.Sprintf("movie|%s|%d|3d", strings.ToLower(title), year)
	}
	return fmt.Sprintf("movie|%s|%d", strings.ToLower(title), year)
}
