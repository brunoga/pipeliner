package store

import "time"

// SeenRecord is stored for every entry that has been processed.
type SeenRecord struct {
	Fingerprint string    `json:"fingerprint"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Task        string    `json:"task"`
	Fields      []string  `json:"fields"`  // which fields were used to compute the fingerprint
	SeenAt      time.Time `json:"seen_at"`
}

// SeenStore tracks which entries have already been processed.
// It operates on opaque fingerprint strings; callers are responsible for
// computing the fingerprint from whichever entry fields identify uniqueness
// for their use case (URL, title, series+episode, torrent hash, etc.).
type SeenStore struct {
	bucket Bucket
}

// NewSeenStore wraps a Bucket as a SeenStore.
func NewSeenStore(b Bucket) *SeenStore {
	return &SeenStore{bucket: b}
}

// IsSeen returns true if the fingerprint has been marked seen before.
func (s *SeenStore) IsSeen(fingerprint string) bool {
	var rec SeenRecord
	found, _ := s.bucket.Get(fingerprint, &rec)
	return found
}

// Mark records the fingerprint as seen with the associated metadata.
func (s *SeenStore) Mark(fingerprint string, rec SeenRecord) error {
	rec.Fingerprint = fingerprint
	if rec.SeenAt.IsZero() {
		rec.SeenAt = time.Now()
	}
	return s.bucket.Put(fingerprint, rec)
}

