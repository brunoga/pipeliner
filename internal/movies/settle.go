package movies

import (
	"fmt"
	"strings"
	"time"
)

// SettleBucketName holds per-title settle timers.
const SettleBucketName = "movies_settle"

// SettleTracker implements a per-title settle window: the delay between first
// seeing a download-worthy release for a title and actually grabbing one.
//
// Releases for a new title arrive in waves — 1080p, then 2160p, then an HDR
// pass, then an Atmos remux, often within hours. Grabbing on sight means
// downloading every rung of that ladder. Delaying each *release* by a fixed
// age does not help: it shifts every rung later by the same amount and you
// still download all of them. The window has to be anchored to the *title*,
// so every release in the wave becomes eligible in the same run and the
// downstream dedup picks the single best one.
type SettleTracker struct {
	b bucket
}

// NewSettleTracker returns a tracker backed by the given bucket.
func NewSettleTracker(b bucket) *SettleTracker { return &SettleTracker{b: b} }

type settleRecord struct {
	FirstSeen time.Time `json:"first_seen"`
}

func settleKey(title string, year int, is3D bool) string {
	if is3D {
		return fmt.Sprintf("%s|%d|3d", strings.ToLower(title), year)
	}
	return fmt.Sprintf("%s|%d", strings.ToLower(title), year)
}

// Remaining reports how much of the settle window is left for a title,
// starting the timer on first call. A non-positive result means the window
// has elapsed and the best available release should be taken now.
func (t *SettleTracker) Remaining(title string, year int, is3D bool, window time.Duration, now time.Time) time.Duration {
	if t == nil || t.b == nil || window <= 0 {
		return 0
	}
	key := settleKey(title, year, is3D)
	var rec settleRecord
	found, err := t.b.Get(key, &rec)
	if err != nil || !found || rec.FirstSeen.IsZero() {
		// First sighting of a wave for this title: start the clock.
		_ = t.b.Put(key, settleRecord{FirstSeen: now})
		return window
	}
	if elapsed := now.Sub(rec.FirstSeen); elapsed < window {
		return window - elapsed
	}
	return 0
}

// Clear ends the settle window for a title, called once a release has been
// downloaded so the next wave starts a fresh timer.
func (t *SettleTracker) Clear(title string, year int, is3D bool) {
	if t == nil || t.b == nil {
		return
	}
	_ = t.b.Delete(settleKey(title, year, is3D))
}
