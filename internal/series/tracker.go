package series

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
)

// Record is persisted for each downloaded episode.
type Record struct {
	SeriesName   string          `json:"series_name"`
	EpisodeID    string          `json:"episode_id"`
	DownloadedAt time.Time       `json:"downloaded_at"`
	Quality      quality.Quality `json:"quality"`
}

// bucket is the minimal key-value interface that Tracker requires.
// store.Bucket satisfies this interface automatically.
type bucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
	Delete(key string) error
	Keys() ([]string, error)
	All() (map[string][]byte, error)
}

// Tracker tracks which episodes of which series have been downloaded.
// It is backed by a bucket so state persists across runs.
// For large libraries consider SQLiteStore which uses a dedicated indexed table.
type Tracker struct {
	bucket bucket
}

// NewTracker wraps a bucket as a series Tracker.
func NewTracker(b bucket) *Tracker {
	return &Tracker{bucket: b}
}

// IsSeen returns true if the given episode has already been downloaded.
func (t *Tracker) IsSeen(seriesName, episodeID string) bool {
	var rec Record
	found, _ := t.bucket.Get(recordKey(seriesName, episodeID), &rec)
	return found
}

// Mark records that an episode has been downloaded.
func (t *Tracker) Mark(r Record) error {
	return t.bucket.Put(recordKey(r.SeriesName, r.EpisodeID), r)
}

// Earliest returns the episode with the lexicographically smallest EpisodeID
// for the given series. Because episode IDs are zero-padded (S01E01, 2023-11-15,
// EP001), lexicographic order matches episode order. This is used by the
// "follow" tracking mode to establish a tracking-start anchor: episodes older
// than the earliest downloaded are rejected in future runs.
func (t *Tracker) Earliest(seriesName string) (*Record, bool) {
	all, err := t.bucket.All()
	if err != nil {
		return nil, false
	}
	prefix := seriesName + "|"
	var earliest *Record
	for k, raw := range all {
		if !hasPrefix(k, prefix) {
			continue
		}
		var rec Record
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.SeriesName != seriesName {
			continue
		}
		if earliest == nil || rec.EpisodeID < earliest.EpisodeID {
			r := rec
			earliest = &r
		}
	}
	return earliest, earliest != nil
}

// Latest returns the most recently downloaded episode for the given series,
// determined by DownloadedAt timestamp. Uses All() to fetch all records in
// a single query rather than Keys() + N×Get().
func (t *Tracker) Latest(seriesName string) (*Record, bool) {
	all, err := t.bucket.All()
	if err != nil {
		return nil, false
	}
	prefix := seriesName + "|"
	var latest *Record
	for k, raw := range all {
		if !hasPrefix(k, prefix) {
			continue
		}
		var rec Record
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.SeriesName != seriesName {
			continue
		}
		if latest == nil || rec.DownloadedAt.After(latest.DownloadedAt) {
			r := rec
			latest = &r
		}
	}
	return latest, latest != nil
}

// EpisodeID returns the canonical episode identifier for an Episode.
// For standard episodes: "S01E01" or "S01E01E02".
// For date episodes: "2023-11-15".
// For absolute episodes: "EP123".
func EpisodeID(ep *Episode) string {
	if ep.IsDate {
		return fmt.Sprintf("%04d-%02d-%02d", ep.Year, ep.Month, ep.Day)
	}
	if ep.Season > 0 {
		if ep.DoubleEpisode > 0 {
			return fmt.Sprintf("S%02dE%02dE%02d", ep.Season, ep.Episode, ep.DoubleEpisode)
		}
		return fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode)
	}
	return fmt.Sprintf("EP%03d", ep.Episode)
}

func recordKey(seriesName, episodeID string) string {
	return seriesName + "|" + episodeID
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
