package series

import (
	"strings"
	"time"
)

// SettleBucketName holds per-episode settle timers.
const SettleBucketName = "series_settle"

// SettleTracker implements a per-episode settle window: the delay between
// first seeing a download-worthy release and actually grabbing one.
//
// Releases arrive in waves — a quick 1080p WEB-DL, then a better encode, then
// a proper — often within hours. Grabbing on sight downloads every rung of
// that ladder. Delaying each *release* by a fixed age only shifts the whole
// ladder later; the window must be anchored to the episode so every release
// in the wave becomes eligible in the same run and the downstream dedup picks
// the single best one.
type SettleTracker struct {
	b bucket
}

// NewSettleTracker returns a tracker backed by the given bucket.
func NewSettleTracker(b bucket) *SettleTracker { return &SettleTracker{b: b} }

type settleRecord struct {
	FirstSeen time.Time `json:"first_seen"`
}

func settleKey(show, episodeID string) string {
	return strings.ToLower(show) + "|" + strings.ToUpper(episodeID)
}

// Remaining reports how much of the settle window is left for an episode,
// starting the timer on first call. A non-positive result means the window
// has elapsed and the best available release should be taken now.
func (t *SettleTracker) Remaining(show, episodeID string, window time.Duration, now time.Time) time.Duration {
	if t == nil || t.b == nil || window <= 0 {
		return 0
	}
	key := settleKey(show, episodeID)
	var rec settleRecord
	found, err := t.b.Get(key, &rec)
	if err != nil || !found || rec.FirstSeen.IsZero() {
		_ = t.b.Put(key, settleRecord{FirstSeen: now})
		return window
	}
	if elapsed := now.Sub(rec.FirstSeen); elapsed < window {
		return window - elapsed
	}
	return 0
}

// Clear ends the settle window for an episode, called once a release has been
// downloaded so the next wave starts a fresh timer.
func (t *SettleTracker) Clear(show, episodeID string) {
	if t == nil || t.b == nil {
		return
	}
	_ = t.b.Delete(settleKey(show, episodeID))
}
