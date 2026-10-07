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
	URL      string `json:"url"`
	InfoHash string `json:"info_hash,omitempty"`
	// StableKeys are the durable identifiers the release was recorded under
	// (see Entry.StableKeys). Kept on the record so a reader can tell which
	// rung matched, and so a record written by an older build — which has
	// none — is still recognisable as such.
	StableKeys []string  `json:"stable_keys,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	FailedAt   time.Time `json:"failed_at"`
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

// MarkFailed records a failed grab under every durable identifier it has, and
// under its URL.
//
// The URL alone is not enough and never was. Jackett re-encrypts its download
// links on every search, so the same release arrives with a different URL each
// run and a URL-keyed blocklist never matches it again — that is how a dead
// torrent with no seeds was re-downloaded nine times, purged by the janitor,
// re-found under a fresh URL and grabbed again.
//
// Recording the info hash fixed that only where an indexer supplies one. Some
// supply none: 3dtorrents returns no infohash attribute at all, so entries
// from it reached the seen filter with nothing durable to match, and the loop
// survived there. The stable keys close that gap — a source-scoped identifier
// such as a Jackett GUID, which is a permalink and does not rotate.
//
// Every key is written, because which one a later run can offer depends on
// where the entry came from and how far down the pipeline it got.
func (s *FailedStore) MarkFailed(infoHash, url, reason string, stableKeys ...string) error {
	rec := FailedRecord{
		URL:        url,
		InfoHash:   strings.ToLower(infoHash),
		StableKeys: stableKeys,
		Reason:     reason,
		FailedAt:   time.Now().UTC(),
	}
	if rec.InfoHash != "" {
		if err := s.bucket.Put(rec.InfoHash, rec); err != nil {
			return err
		}
	}
	for _, k := range stableKeys {
		if k == "" {
			continue
		}
		if err := s.bucket.Put(k, rec); err != nil {
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

// Lookup finds a failed record by the strongest identifier the caller can
// offer, in descending order of durability: the info hash, then any
// source-scoped stable key, then the URL.
//
// The order is the point. The hash identifies a release globally; a stable key
// identifies it within its source; the URL identifies only the exact link that
// failed, which for an indexer proxy means the request that produced it and
// nothing more.
func (s *FailedStore) Lookup(infoHash, url string, stableKeys ...string) (*FailedRecord, bool) {
	if h := strings.ToLower(infoHash); h != "" {
		if rec, ok := s.Get(h); ok {
			return rec, true
		}
	}
	for _, k := range stableKeys {
		if k == "" {
			continue
		}
		if rec, ok := s.Get(k); ok {
			return rec, true
		}
	}
	if url == "" {
		return nil, false
	}
	return s.Get(url)
}
