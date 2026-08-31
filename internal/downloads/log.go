// Package downloads maintains an append-only audit log of confirmed downloads.
//
// The series and movies trackers keep exactly one record per episode/movie — a
// quality upgrade overwrites it — so they cannot answer "was this downloaded
// more than once, and how did the quality change?". This log records one event
// every time a download is committed, so that history is preserved going
// forward. It is written from the same commit path that advances the trackers,
// so an entry is logged only when the full pipeline (including the sink)
// confirmed the download.
package downloads

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
)

// BucketName is the store bucket holding the download audit log.
const BucketName = "download_log"

// Event is one confirmed download.
type Event struct {
	// MediaType is "series" or "movie".
	MediaType string `json:"media_type"`
	// Name is the normalized tracker key name (matches the series/movies
	// tracker's stored name).
	Name string `json:"name"`
	// DisplayName is the human-readable title as downloaded.
	DisplayName string `json:"display_name,omitempty"`
	// EpisodeID is set for series events (e.g. "S04E05").
	EpisodeID string `json:"episode_id,omitempty"`
	// Year and Is3D are set for movie events.
	Year int  `json:"year,omitempty"`
	Is3D bool `json:"is_3d,omitempty"`
	// Quality is the release quality that was downloaded.
	Quality quality.Quality `json:"quality"`
	// Repack reports whether the release was a PROPER/REPACK.
	Repack bool `json:"repack,omitempty"`
	// DownloadedAt is when the download was committed.
	DownloadedAt time.Time `json:"downloaded_at"`
	// Task is the pipeline that produced the download.
	Task string `json:"task,omitempty"`
}

// bucket is the minimal store interface the log needs.
type bucket interface {
	Put(key string, value any) error
	All() (map[string][]byte, error)
}

// Log is an append-only download history backed by a store bucket.
type Log struct{ bucket bucket }

// New wraps a bucket as a download Log.
func New(b bucket) *Log { return &Log{bucket: b} }

// itemKey identifies the media item an event is about (independent of when it
// was downloaded), so multiple downloads of the same episode/movie group
// together.
func (e Event) itemKey() string {
	if e.MediaType == "movie" {
		suffix := ""
		if e.Is3D {
			suffix = "|3d"
		}
		return fmt.Sprintf("movie|%s|%d%s", e.Name, e.Year, suffix)
	}
	return fmt.Sprintf("series|%s|%s", e.Name, e.EpisodeID)
}

// Append records one download event. The store key embeds the item key and the
// download timestamp so repeated downloads of the same item (quality upgrades)
// are all retained rather than overwriting one another.
func (l *Log) Append(e Event) error {
	if e.DownloadedAt.IsZero() {
		e.DownloadedAt = time.Now()
	}
	key := fmt.Sprintf("%s|%020d", e.itemKey(), e.DownloadedAt.UnixNano())
	return l.bucket.Put(key, e)
}

// Query returns all events whose Name or DisplayName contains q
// (case-insensitive), newest first. A blank query matches nothing.
func (l *Log) Query(q string) ([]Event, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" {
		return nil, nil
	}
	events, err := l.all()
	if err != nil {
		return nil, err
	}
	out := events[:0]
	for _, e := range events {
		if strings.Contains(strings.ToLower(e.Name), needle) ||
			strings.Contains(strings.ToLower(e.DisplayName), needle) {
			out = append(out, e)
		}
	}
	sortByNewest(out)
	return out, nil
}

// all decodes every event in the bucket (unsorted).
func (l *Log) all() ([]Event, error) {
	raw, err := l.bucket.All()
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(raw))
	for _, v := range raw {
		var e Event
		if err := json.Unmarshal(v, &e); err != nil {
			continue // skip malformed rows rather than failing the whole query
		}
		out = append(out, e)
	}
	return out, nil
}

func sortByNewest(events []Event) {
	sort.Slice(events, func(i, j int) bool { return events[i].DownloadedAt.After(events[j].DownloadedAt) })
}

// ItemHistory is all downloads of one media item, newest first. A Count greater
// than 1 means the item was downloaded more than once — typically a quality
// upgrade, visible in the Downloads' quality progression.
type ItemHistory struct {
	MediaType   string    `json:"media_type"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name,omitempty"`
	EpisodeID   string    `json:"episode_id,omitempty"`
	Year        int       `json:"year,omitempty"`
	Is3D        bool      `json:"is_3d,omitempty"`
	Count       int       `json:"count"`
	LastAt      time.Time `json:"last_at"`
	Downloads   []Event   `json:"downloads"`
}

// GroupByItem collapses a flat event list into per-item histories, each with
// its downloads newest-first, and orders the items by most-recent download
// first. The quality upgrade story reads straight off Downloads.
func GroupByItem(events []Event) []ItemHistory {
	byKey := map[string]*ItemHistory{}
	var order []string
	for _, e := range events {
		k := e.itemKey()
		h, ok := byKey[k]
		if !ok {
			h = &ItemHistory{
				MediaType: e.MediaType, Name: e.Name, DisplayName: e.DisplayName,
				EpisodeID: e.EpisodeID, Year: e.Year, Is3D: e.Is3D,
			}
			byKey[k] = h
			order = append(order, k)
		}
		if e.DisplayName != "" {
			h.DisplayName = e.DisplayName // prefer a non-empty display name
		}
		h.Downloads = append(h.Downloads, e)
	}
	out := make([]ItemHistory, 0, len(order))
	for _, k := range order {
		h := byKey[k]
		sortByNewest(h.Downloads)
		h.Count = len(h.Downloads)
		if h.Count > 0 {
			h.LastAt = h.Downloads[0].DownloadedAt
		}
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out
}
