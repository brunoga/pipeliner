// Package movies provides movie tracking for deduplication across pipeline runs.
package movies

import (
	"fmt"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/quality"
)

// TrackerBucketName is the store bucket holding per-movie download records.
// Like the series tracker bucket it is deliberately not namespaced by task:
// the movies filter and the mark_failed sink across all pipelines share one
// tracker.
const TrackerBucketName = "movies"

// Record is persisted for each downloaded movie.
type Record struct {
	Title        string          `json:"title"`
	Year         int             `json:"year"`
	Is3D         bool            `json:"is_3d,omitempty"`
	Repack       bool            `json:"repack,omitempty"`
	DownloadedAt time.Time       `json:"downloaded_at"`
	Quality      quality.Quality `json:"quality"`

	// Prev is the record this one replaced: one level of undo, so a failed
	// grab can be rolled back to the download that preceded it instead of
	// erasing the movie's history. Nil for a first download.
	Prev *Previous `json:"prev,omitempty"`
}

// Previous is a superseded download, kept on the record that replaced it so
// UntrackGrab can restore it. Only the fields the upgrade decision reads are
// retained — title/year/is3D are the key and cannot differ.
type Previous struct {
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

// Mark records that a movie has been downloaded. Any record already stored
// under the same key is carried along as r.Prev so UntrackGrab can roll back
// to it; only one level is kept, which is all failed-grab recovery needs (it
// always refers to the most recent grab of a release).
func (t *Tracker) Mark(r Record) error {
	if r.DownloadedAt.IsZero() {
		r.DownloadedAt = time.Now()
	}
	key := recordKey(r.Title, r.Year, r.Is3D)
	if r.Prev == nil {
		var old Record
		if found, _ := t.bucket.Get(key, &old); found {
			r.Prev = &Previous{
				Repack:       old.Repack,
				DownloadedAt: old.DownloadedAt,
				Quality:      old.Quality,
			}
		}
	}
	return t.bucket.Put(key, r)
}

// Forget removes the record for a given movie outright, history and all.
// 3D and non-3D versions are tracked independently. This is the explicit
// user-driven un-track (CLI, web tools); failed-grab recovery wants
// UntrackGrab instead.
func (t *Tracker) Forget(title string, year int, is3D bool) error {
	return t.bucket.Delete(recordKey(title, year, is3D))
}

// UntrackOutcome reports what UntrackGrab did.
type UntrackOutcome int

const (
	// UntrackNoRecord: nothing was stored under the key.
	UntrackNoRecord UntrackOutcome = iota
	// UntrackStale: the stored record describes a different download than the
	// failed grab, so it was left alone.
	UntrackStale
	// UntrackRestored: the record was rolled back to the download it replaced.
	UntrackRestored
	// UntrackDeleted: the failed grab was the only download on record, so the
	// record was removed.
	UntrackDeleted
)

func (o UntrackOutcome) String() string {
	switch o {
	case UntrackStale:
		return "left (record is from a later download)"
	case UntrackRestored:
		return "rolled back to previous download"
	case UntrackDeleted:
		return "deleted"
	default:
		return "no record"
	}
}

// UntrackGrab rolls back the tracker after a grab turned out to be dead.
//
// Plain deletion is wrong here: the tracker holds one record per movie and
// Mark overwrites it, so deleting it also erases the memory of whatever was
// downloaded before — and the next run then re-downloads a film that is
// already in the library, often at worse quality than the copy it has.
// Instead the record is rolled back to the download it replaced, and only
// deleted when the failed grab is the only one on record.
//
// failed is the quality of the grab that died, as recorded on its grab record.
// When it does not match the stored record the record belongs to a later,
// different download and is left untouched. Pass hasQuality=false for grab
// records written before the quality was captured: the rollback still happens,
// but the staleness check cannot run.
func (t *Tracker) UntrackGrab(title string, year int, is3D bool, failed quality.Quality, hasQuality bool) (UntrackOutcome, error) {
	key := recordKey(title, year, is3D)
	var rec Record
	found, err := t.bucket.Get(key, &rec)
	if err != nil {
		return UntrackNoRecord, err
	}
	if !found {
		return UntrackNoRecord, nil
	}
	if hasQuality && rec.Quality != failed {
		return UntrackStale, nil
	}
	if rec.Prev == nil {
		return UntrackDeleted, t.bucket.Delete(key)
	}
	restored := Record{
		Title:        rec.Title,
		Year:         rec.Year,
		Is3D:         rec.Is3D,
		Repack:       rec.Prev.Repack,
		DownloadedAt: rec.Prev.DownloadedAt,
		Quality:      rec.Prev.Quality,
	}
	return UntrackRestored, t.bucket.Put(key, restored)
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
//
// A year of 0 on either side means "unknown" — the release lacked a year when
// it was recorded, or the incoming entry lacks one now — and is treated as
// compatible rather than letting the drift check defeat dedup. Without this, a
// record stored with year 0 (e.g. a first download before the year could be
// enriched) would be invisible to a later IsSeen carrying a real year, causing
// a re-download.
func (t *Tracker) LatestNearYear(title string, year int, is3D bool) (*Record, bool) {
	return t.latestMatching(title, is3D, func(recYear int) bool {
		if recYear == 0 || year == 0 {
			return true
		}
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
