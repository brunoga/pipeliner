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
	Repack       bool            `json:"repack,omitempty"`
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

// yearDriftTolerance is the maximum allowed difference (in years) between an
// incoming entry's year and a stored record's year for them to be treated as
// the same movie. Covers theatrical vs. home-video release-year drift — the
// same release names a film by either the festival/theatrical year or the
// Blu-ray year depending on the encode (e.g. Good Boy 2025 theatrical /
// 2026 Blu-ray).
const yearDriftTolerance = 1

// IsSeen returns true if the given movie has already been downloaded.
// 3D and non-3D versions are tracked independently. Two fallbacks cover
// cases where the exact-key lookup misses but a related record exists:
//   - year == 0 (no year in the release filename): scan all title+is3D
//     records, so a previously-stored real year from TMDb/Trakt enrichment
//     still gates the entry.
//   - year != 0 but no exact match: scan for a record within
//     ±yearDriftTolerance, so theatrical/home-video drift doesn't defeat
//     dedup.
func (t *Tracker) IsSeen(title string, year int, is3D bool) bool {
	var rec Record
	if found, _ := t.bucket.Get(recordKey(title, year, is3D), &rec); found {
		return true
	}
	if year == 0 {
		_, found := t.Latest(title, is3D)
		return found
	}
	_, found := t.LatestNearYear(title, year, is3D)
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
	return t.latestMatching(title, is3D, func(int) bool { return true })
}

// LatestNearYear is like Latest but restricts the scan to records whose
// stored year is within ±yearDriftTolerance of the given year. Use this
// (instead of Latest) when comparing an incoming entry against a tracked
// one so theatrical/home-video drift is treated as the same movie.
func (t *Tracker) LatestNearYear(title string, year int, is3D bool) (*Record, bool) {
	return t.latestMatching(title, is3D, func(recYear int) bool {
		diff := recYear - year
		if diff < 0 {
			diff = -diff
		}
		return diff <= yearDriftTolerance
	})
}

func (t *Tracker) latestMatching(title string, is3D bool, yearOK func(int) bool) (*Record, bool) {
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
		if !yearOK(rec.Year) {
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
