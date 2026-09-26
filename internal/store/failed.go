package store

import (
	"strings"
	"time"
)

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
	InfoHash string    `json:"info_hash,omitempty"`
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

// MarkFailed records a failed grab under both its info hash and its URL.
//
// The info hash is the one durable identity a release has. Indexer proxy
// URLs are not: Jackett re-encrypts its download links on every search, so
// the same release arrives with a different URL each run and a URL-keyed
// blocklist never matches it again. That is how a dead torrent with no seeds
// was re-downloaded nine times — purged by the janitor, re-found under a
// fresh URL, grabbed again. Recording both keys means the blocklist still
// works for entries that reach the seen filter before their hash is known,
// and for records written before hashes were stored.
func (s *FailedStore) MarkFailed(infoHash, url, reason string) error {
	rec := FailedRecord{
		URL:      url,
		InfoHash: strings.ToLower(infoHash),
		Reason:   reason,
		FailedAt: time.Now().UTC(),
	}
	if rec.InfoHash != "" {
		if err := s.bucket.Put(rec.InfoHash, rec); err != nil {
			return err
		}
	}
	if url == "" {
		return nil
	}
	return s.bucket.Put(url, rec)
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

// Lookup finds a failed record by info hash first and URL second. Prefer it
// over Get: the hash identifies the release across the rotating proxy URLs
// indexers hand out, while the URL only matches the exact link that failed.
func (s *FailedStore) Lookup(infoHash, url string) (*FailedRecord, bool) {
	if h := strings.ToLower(infoHash); h != "" {
		if rec, ok := s.Get(h); ok {
			return rec, true
		}
	}
	if url == "" {
		return nil, false
	}
	return s.Get(url)
}
