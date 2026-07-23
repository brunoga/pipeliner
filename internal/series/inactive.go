package series

import "time"

// InactiveBucketName is the store bucket holding per-show inactive flags.
// It is a parallel bucket keyed by normalized show name rather than a field
// on the per-episode Record: the flag is per-show, so storing it once avoids
// rewriting every episode record (and needing a schema migration) on each
// deactivate/reactivate. Like TrackerBucketName it is shared across all
// tasks so any pipeline can deactivate a show for every other pipeline.
const InactiveBucketName = "series_inactive"

// InactiveRecord marks a tracked show as inactive. The series filter rejects
// episodes of inactive shows early, before any quality or tracker checks.
type InactiveRecord struct {
	// Reason is a short human-readable cause, e.g. "complete". It is embedded
	// in the series filter's rejection reason: "series: <show> inactive (<reason>)".
	Reason        string    `json:"reason,omitempty"`
	DeactivatedAt time.Time `json:"deactivated_at"`
}

// InactiveSet tracks which shows have been deactivated. It is backed by a
// bucket so state persists across runs and is visible to every task.
type InactiveSet struct {
	bucket bucket
}

// NewInactiveSet wraps a bucket as an InactiveSet.
func NewInactiveSet(b bucket) *InactiveSet {
	return &InactiveSet{bucket: b}
}

// Deactivate marks a show as inactive with the given reason.
func (s *InactiveSet) Deactivate(name, reason string) error {
	return s.bucket.Put(name, InactiveRecord{Reason: reason, DeactivatedAt: time.Now()})
}

// Reactivate removes a show's inactive flag. No-op if the show is active.
func (s *InactiveSet) Reactivate(name string) error {
	return s.bucket.Delete(name)
}

// Get returns the inactive record for a show, if it is inactive.
// A nil InactiveSet is treated as "nothing deactivated" so callers
// constructed without a bucket (tests, partial wiring) stay safe.
func (s *InactiveSet) Get(name string) (*InactiveRecord, bool) {
	if s == nil || s.bucket == nil {
		return nil, false
	}
	var rec InactiveRecord
	found, _ := s.bucket.Get(name, &rec)
	if !found {
		return nil, false
	}
	return &rec, true
}

// IsInactive reports whether the show has been deactivated.
func (s *InactiveSet) IsInactive(name string) bool {
	_, ok := s.Get(name)
	return ok
}
