// Package movies provides movie tracking for deduplication across pipeline runs.
package movies

import (
	"fmt"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
)

// Record is persisted for each downloaded movie.
type Record struct {
	Title        string          `json:"title"`
	Year         int             `json:"year"`
	DownloadedAt time.Time       `json:"downloaded_at"`
	Quality      quality.Quality `json:"quality"`
}

// bucket is the minimal key-value interface that Tracker requires.
type bucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
	Delete(key string) error
	Keys() ([]string, error)
}

// Tracker tracks which movies have been downloaded.
type Tracker struct {
	bucket bucket
}

// NewTracker wraps a bucket as a movie Tracker.
func NewTracker(b bucket) *Tracker {
	return &Tracker{bucket: b}
}

// IsSeen returns true if the given movie has already been downloaded.
func (t *Tracker) IsSeen(title string, year int) bool {
	var rec Record
	found, _ := t.bucket.Get(recordKey(title, year), &rec)
	return found
}

// Mark records that a movie has been downloaded.
func (t *Tracker) Mark(r Record) error {
	if r.DownloadedAt.IsZero() {
		r.DownloadedAt = time.Now()
	}
	return t.bucket.Put(recordKey(r.Title, r.Year), r)
}

// Forget removes the record for a given movie.
func (t *Tracker) Forget(title string, year int) error {
	return t.bucket.Delete(recordKey(title, year))
}

// Latest returns the most recently downloaded record for a movie by title (any year).
func (t *Tracker) Latest(title string) (*Record, bool) {
	keys, err := t.bucket.Keys()
	if err != nil {
		return nil, false
	}
	norm := strings.ToLower(title)
	var latest *Record
	for _, k := range keys {
		var rec Record
		if found, _ := t.bucket.Get(k, &rec); !found {
			continue
		}
		if strings.ToLower(rec.Title) != norm {
			continue
		}
		if latest == nil || rec.DownloadedAt.After(latest.DownloadedAt) {
			r := rec
			latest = &r
		}
	}
	return latest, latest != nil
}

func recordKey(title string, year int) string {
	return fmt.Sprintf("%s|%d", strings.ToLower(title), year)
}
