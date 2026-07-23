// Package grabs records which release URL each added torrent came from, so
// janitor pipelines can walk back from a torrent in a download client's
// session (identified only by info-hash) to the release that produced it.
//
// The transmission and qbittorrent sinks write one record per successful
// torrent-add; the mark_failed sink resolves session entries through the
// bucket to mark the original release URL failed and to un-track the
// episode/movie in the series/movies trackers.
package grabs

import (
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/magnet"
)

// BucketName is the store bucket holding hash → grab records. Like the seen
// and tracker buckets it is deliberately not namespaced by task: a janitor
// pipeline must resolve torrents added by any other pipeline.
const BucketName = "grabs"

// Record links a torrent info-hash to the release it was grabbed from,
// plus enough tracker-key context to un-track the content on failure.
type Record struct {
	// URL is the release URL the download sink received (magnet or .torrent).
	URL string `json:"url"`
	// Title is the entry's display title at add time.
	Title string `json:"title,omitempty"`
	// Task is the pipeline that performed the add.
	Task    string    `json:"task,omitempty"`
	AddedAt time.Time `json:"added_at"`

	// SeriesName/EpisodeID are the series tracker key, present when the entry
	// passed through the series filter before the sink.
	SeriesName string `json:"series_name,omitempty"`
	EpisodeID  string `json:"episode_id,omitempty"`

	// MovieTitle/MovieYear/MovieIs3D are the movies tracker key, present when
	// the entry passed through the movies filter before the sink.
	MovieTitle string `json:"movie_title,omitempty"`
	MovieYear  int    `json:"movie_year,omitempty"`
	MovieIs3D  bool   `json:"movie_is_3d,omitempty"`
}

// bucket is the minimal key-value interface Store requires; store.Bucket
// satisfies it automatically.
type bucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
	Delete(key string) error
}

// Store persists grab records keyed by lowercase hex info-hash.
type Store struct {
	bucket bucket
}

// NewStore wraps a bucket as a grab-record Store.
func NewStore(b bucket) *Store {
	return &Store{bucket: b}
}

// Put stores the record under the (lowercased) info-hash.
func (s *Store) Put(hash string, r Record) error {
	if r.AddedAt.IsZero() {
		r.AddedAt = time.Now()
	}
	return s.bucket.Put(strings.ToLower(hash), r)
}

// Get returns the record for an info-hash, if one was stored.
func (s *Store) Get(hash string) (*Record, bool) {
	var rec Record
	found, _ := s.bucket.Get(strings.ToLower(hash), &rec)
	if !found {
		return nil, false
	}
	return &rec, true
}

// Delete removes the record for an info-hash. No-op if absent.
func (s *Store) Delete(hash string) error {
	return s.bucket.Delete(strings.ToLower(hash))
}

// FromEntry builds a Record from an entry at torrent-add time, capturing the
// release URL plus whatever tracker keys the upstream filters stamped.
func FromEntry(e *entry.Entry, task string) Record {
	return Record{
		URL:        e.URL,
		Title:      e.Title,
		Task:       task,
		SeriesName: e.GetString(entry.FieldSeriesTrackerName),
		EpisodeID:  e.GetString(entry.FieldSeriesEpisodeID),
		MovieTitle: e.GetString(entry.FieldMoviesTrackerTitle),
		MovieYear:  e.GetInt(entry.FieldVideoYear),
		MovieIs3D:  e.GetBool(entry.FieldVideoIs3D),
	}
}

// HashForEntry determines the entry's torrent info-hash without any network
// round-trip: the torrent_info_hash field when a metainfo plugin or the
// source set it, otherwise parsed from a magnet URL. Returns "" (lowercase
// hex otherwise) when the hash cannot be determined locally — e.g. a bare
// .torrent URL that was never run through metainfo_torrent.
func HashForEntry(e *entry.Entry) string {
	if h := e.GetString(entry.FieldTorrentInfoHash); h != "" {
		return strings.ToLower(h)
	}
	if strings.HasPrefix(e.URL, "magnet:") {
		if m, err := magnet.Parse(e.URL); err == nil {
			return m.InfoHash
		}
	}
	return ""
}
