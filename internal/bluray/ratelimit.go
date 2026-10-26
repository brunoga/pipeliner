package bluray

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// JitterRateLimiter enforces a minimum delay plus random jitter between consecutive requests.
type JitterRateLimiter struct {
	mu          sync.Mutex
	minInterval time.Duration
	maxJitter   time.Duration
	lastRequest time.Time
}

// NewJitterRateLimiter creates a rate limiter that waits at least minInterval + [0, maxJitter)
// between consecutive calls to Wait.
func NewJitterRateLimiter(minInterval, maxJitter time.Duration) *JitterRateLimiter {
	return &JitterRateLimiter{
		minInterval: minInterval,
		maxJitter:   maxJitter,
	}
}

// Wait blocks until the required pacing interval has elapsed or ctx is canceled.
func (r *JitterRateLimiter) Wait(ctx context.Context) error {
	if r == nil || (r.minInterval <= 0 && r.maxJitter <= 0) {
		return nil
	}

	r.mu.Lock()
	now := time.Now()
	var waitDuration time.Duration
	if !r.lastRequest.IsZero() {
		delay := r.minInterval
		if r.maxJitter > 0 {
			delay += time.Duration(rand.Int64N(int64(r.maxJitter))) //nolint:gosec // G404: non-cryptographic jitter for HTTP pacing
		}
		target := r.lastRequest.Add(delay)
		if target.After(now) {
			waitDuration = target.Sub(now)
		}
	}
	r.lastRequest = now.Add(waitDuration)
	r.mu.Unlock()

	if waitDuration <= 0 {
		return nil
	}

	timer := time.NewTimer(waitDuration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
