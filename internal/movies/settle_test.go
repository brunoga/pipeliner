package movies

import (
	"encoding/json"
	"testing"
	"time"
)

// settleBucket is an in-memory bucket that round-trips through JSON, matching
// how the real store serialises values.
type settleBucket struct{ data map[string][]byte }

func newSettleBucket() *settleBucket { return &settleBucket{data: map[string][]byte{}} }

func (b *settleBucket) Put(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b.data[key] = raw
	return nil
}

func (b *settleBucket) Get(key string, dest any) (bool, error) {
	raw, ok := b.data[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dest)
}

func (b *settleBucket) Delete(key string) error { delete(b.data, key); return nil }

func (b *settleBucket) Keys() ([]string, error) {
	keys := make([]string, 0, len(b.data))
	for k := range b.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func TestSettleTrackerStartsAndElapses(t *testing.T) {
	b := newSettleBucket()
	st := NewSettleTracker(b)
	now := time.Now()
	window := 12 * time.Hour

	// First sighting starts the clock and reports the full window.
	if left := st.Remaining("heat", 1995, false, window, now); left != window {
		t.Errorf("first sighting: got %v, want %v", left, window)
	}
	// Partway through, the remainder shrinks.
	if left := st.Remaining("heat", 1995, false, window, now.Add(4*time.Hour)); left != 8*time.Hour {
		t.Errorf("mid-window: got %v, want 8h", left)
	}
	// Past the window, nothing is left — take the best release now.
	if left := st.Remaining("heat", 1995, false, window, now.Add(13*time.Hour)); left > 0 {
		t.Errorf("after the window: got %v, want <= 0", left)
	}
	// Clearing restarts the next wave from scratch.
	st.Clear("heat", 1995, false)
	if left := st.Remaining("heat", 1995, false, window, now.Add(20*time.Hour)); left != window {
		t.Errorf("after clear: got %v, want a fresh %v", left, window)
	}
}

func TestSettleTrackerKeysSeparate3DAndYear(t *testing.T) {
	st := NewSettleTracker(newSettleBucket())
	now := time.Now()
	w := time.Hour
	st.Remaining("heat", 1995, false, w, now)
	// A different year, or the 3D edition, is a distinct wave.
	if left := st.Remaining("heat", 1995, true, w, now); left != w {
		t.Error("3D edition should have its own window")
	}
	if left := st.Remaining("heat", 2024, false, w, now); left != w {
		t.Error("a different year should have its own window")
	}
}

// A zero window disables the mechanism entirely, and a nil tracker is safe.
func TestSettleTrackerDisabled(t *testing.T) {
	st := NewSettleTracker(newSettleBucket())
	if left := st.Remaining("x", 2024, false, 0, time.Now()); left != 0 {
		t.Errorf("zero window must disable settling, got %v", left)
	}
	var nilTracker *SettleTracker
	if left := nilTracker.Remaining("x", 2024, false, time.Hour, time.Now()); left != 0 {
		t.Errorf("nil tracker must be safe, got %v", left)
	}
	nilTracker.Clear("x", 2024, false) // must not panic
}
