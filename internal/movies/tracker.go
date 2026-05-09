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
	Is3D         bool            `json:"is_3d,omitempty"`
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
// 3D and non-3D versions are tracked independently.
// When year is 0 (not present in the release filename), the exact-key lookup
// will miss records that were stored with the real year from TMDb/TMDb enrichment
// (which runs after the filter phase). In that case we fall back to a full
// title+is3D scan so yearless filenames are still gated correctly.
func (t *Tracker) IsSeen(title string, year int, is3D bool) bool {
	var rec Record
	if found, _ := t.bucket.Get(recordKey(title, year, is3D), &rec); found {
		return true
	}
	if year != 0 {
		return false
	}
	// year unknown — scan for any record with matching title and 3D flag
	_, found := t.Latest(title, is3D)
	return found
}

// Mark records that a movie has been downloaded.
func (t *Tracker) Mark(r Record) error {
	if r.DownloadedAt.IsZero() {
		r.DownloadedAt = time.Now()
	}
	return t.bucket.Put(recordKey(r.Title, r.Year, r.Is3D), r)
}

// Forget removes the record for a given movie.
// 3D and non-3D versions are tracked independently.
func (t *Tracker) Forget(title string, year int, is3D bool) error {
	return t.bucket.Delete(recordKey(title, year, is3D))
}

// Latest returns the most recently downloaded record for a movie by title,
// matching the given 3D status. 3D and non-3D versions are tracked independently.
func (t *Tracker) Latest(title string, is3D bool) (*Record, bool) {
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
		if rec.Is3D != is3D {
			continue
		}
		if latest == nil || rec.DownloadedAt.After(latest.DownloadedAt) {
			r := rec
			latest = &r
		}
	}
	return latest, latest != nil
}

func recordKey(title string, year int, is3D bool) string {
	if is3D {
		return fmt.Sprintf("%s|%d|3d", strings.ToLower(title), year)
	}
	return fmt.Sprintf("%s|%d", strings.ToLower(title), year)
}
