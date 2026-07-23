package store

import "time"

// FailedBucketName is the store bucket holding failed-grab release URLs.
// It is a parallel bucket to "seen" (same shared-across-tasks pattern as
// series.InactiveBucketName): keys are raw release URLs, not fingerprints,
// so any pipeline can mark or check a URL without knowing which fingerprint
// fields the original seen filter was configured with. The seen filter
// rejects URLs present here when retry_failed=true, guaranteeing the exact
// failed release is never re-grabbed even after its tracker records are
// forgotten.
const FailedBucketName = "seen_failed"

// FailedRecord is stored for every release URL whose grab failed after the
// download was already confirmed (dead/stalled/errored torrent detected by a
// janitor pipeline).
type FailedRecord struct {
	URL      string    `json:"url"`
	Reason   string    `json:"reason,omitempty"`
	FailedAt time.Time `json:"failed_at"`
}

// FailedStore tracks release URLs whose grabs failed. Backed by a bucket so
// state persists across runs and is visible to every task.
type FailedStore struct {
	bucket Bucket
}

// NewFailedStore wraps a Bucket as a FailedStore.
func NewFailedStore(b Bucket) *FailedStore {
	return &FailedStore{bucket: b}
}

// MarkFailed records the URL as a failed grab with the given reason.
// Re-marking an already-failed URL overwrites the record.
func (s *FailedStore) MarkFailed(url, reason string) error {
	return s.bucket.Put(url, FailedRecord{
		URL:      url,
		Reason:   reason,
		FailedAt: time.Now(),
	})
}

// Get returns the failed record for a URL, if the URL was marked failed.
func (s *FailedStore) Get(url string) (*FailedRecord, bool) {
	var rec FailedRecord
	found, _ := s.bucket.Get(url, &rec)
	if !found {
		return nil, false
	}
	return &rec, true
}

// IsFailed reports whether the URL was marked as a failed grab.
func (s *FailedStore) IsFailed(url string) bool {
	_, ok := s.Get(url)
	return ok
}
