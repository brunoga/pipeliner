package web

import "testing"

// TestBroadcasterAbsorbsBurst: a burst up to the subscriber buffer depth must
// reach a registered subscriber without any Publish dropping — the property a
// large pipeline run's log spike relies on.
func TestBroadcasterAbsorbsBurst(t *testing.T) {
	b := NewBroadcaster()
	_, ch, _ := b.Subscribe(0)

	for i := 0; i < subscriberBuffer; i++ {
		b.Publish("line", int64(i+1))
	}

	got := 0
	for {
		select {
		case <-ch:
			got++
		default:
			if got != subscriberBuffer {
				t.Fatalf("dropped within buffer: received %d/%d", got, subscriberBuffer)
			}
			return
		}
	}
}

// TestBroadcasterRingRetainsNewestOnOverflow: past the buffer, the live channel
// drops (the writer will have reconnected/bridged), but the in-memory ring still
// holds the newest recentRing events so a reconnecting client can replay them.
func TestBroadcasterRingRetainsNewestOnOverflow(t *testing.T) {
	b := NewBroadcaster()
	_, _, _ = b.Subscribe(0) // registered but never drained → its channel fills

	total := int64(subscriberBuffer + recentRing + 100)
	for i := int64(1); i <= total; i++ {
		b.Publish("l", i)
	}

	snap := b.snapAfter(0)
	if len(snap) != recentRing {
		t.Fatalf("ring length = %d, want %d", len(snap), recentRing)
	}
	// The ring holds the newest events, ending at the last published cursor.
	if newest := snap[len(snap)-1]; newest.Pos.ByteEnd != total {
		t.Errorf("ring newest cursor = %d, want %d", newest.Pos.ByteEnd, total)
	}
	if oldest := snap[0]; oldest.Pos.ByteEnd != total-int64(recentRing)+1 {
		t.Errorf("ring oldest cursor = %d, want %d", oldest.Pos.ByteEnd, total-int64(recentRing)+1)
	}
}
