package web

import (
	"sync"
)

// recentRing is the size of the broadcaster's small in-memory ring used
// to bridge SSE reconnects via Last-Event-ID. Larger reconnect gaps are
// bridged by the client calling /api/logs/after with the cursor of its
// last-rendered live line.
const recentRing = 256

// LogEvent is one log record forwarded to SSE subscribers. Pos is the
// stable file cursor assigned by the rotating writer; Seq is a monotonic
// per-process counter used as the SSE event id so Last-Event-ID can
// resume short disconnections.
type LogEvent struct {
	Seq  int64
	Pos  LinePos
	Text string
}

// Broadcaster fans LogEvents out to live subscribers and keeps the most
// recent recentRing events so a freshly-reconnected client can replay
// the gap. The zero value is unusable; build via NewBroadcaster.
type Broadcaster struct {
	mu      sync.Mutex
	clients map[chan LogEvent]struct{}
	recent  []LogEvent // ring; len up to recentRing
	wpos    int        // next write index when ring is full
	seq     int64
	// rotationSeq is bumped when the file writer reports a rotation. We
	// don't store rotation events in the ring; instead, the SSE writer
	// observes the latest rotationSeq vs what it last delivered and
	// emits a "rotate" SSE event so clients refresh their cursors.
	rotationSeq int64
}

// NewBroadcaster returns an empty Broadcaster ready for use.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan LogEvent]struct{}),
		recent:  make([]LogEvent, 0, recentRing),
	}
}

// Publish forwards one line + its file cursor to all subscribers and
// stores it in the reconnect ring. Pos.FileIdx is always 0 at write time
// (it's the active base file); older cursors served by /api/logs/before
// have higher FileIdx values once rotation has happened.
func (b *Broadcaster) Publish(text string, byteEnd int64) {
	b.mu.Lock()
	b.seq++
	ev := LogEvent{
		Seq:  b.seq,
		Pos:  LinePos{FileIdx: 0, ByteEnd: byteEnd},
		Text: text,
	}
	b.appendRecent(ev)
	for ch := range b.clients {
		select {
		case ch <- ev:
		default:
			// Slow consumer — drop. They'll reconnect and replay via the
			// ring (and bridge the rest via /api/logs/after).
		}
	}
	b.mu.Unlock()
}

// NotifyRotate is wired to logfile.Writer.OnRotate. It bumps the
// rotation counter so SSE subscribers can be informed and refresh their
// in-memory cursors against the new file layout.
func (b *Broadcaster) NotifyRotate() {
	b.mu.Lock()
	b.rotationSeq++
	// Shift any in-ring positions: every retained event was in what is
	// now file index 1 (assuming MaxArchives ≥ 1). We bump FileIdx so a
	// reconnect served from the ring lands at the correct (post-rotation)
	// cursor. Clients can also refetch via /api/logs/tail to verify.
	for i := range b.recent {
		b.recent[i].Pos.FileIdx++
	}
	// Inform live subscribers via a special sentinel event: Seq=current,
	// Text="" with Pos.FileIdx == -1 conventionally signals rotation.
	notify := LogEvent{Seq: b.seq, Pos: LinePos{FileIdx: -1}, Text: ""}
	for ch := range b.clients {
		select {
		case ch <- notify:
		default:
		}
	}
	b.mu.Unlock()
}

// Subscribe atomically snapshots events with seq > afterSeq and
// registers a live channel. afterSeq=0 replays the whole ring. Returns
// the snapshot, the live channel, and the current rotation counter so
// the SSE handler can detect rotation that happened mid-disconnect.
func (b *Broadcaster) Subscribe(afterSeq int64) (snapshot []LogEvent, live chan LogEvent, rotation int64) {
	live = make(chan LogEvent, 64)
	b.mu.Lock()
	snapshot = b.snapAfter(afterSeq)
	b.clients[live] = struct{}{}
	rotation = b.rotationSeq
	b.mu.Unlock()
	return
}

// Unsubscribe removes a previously subscribed channel.
func (b *Broadcaster) Unsubscribe(ch chan LogEvent) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *Broadcaster) appendRecent(ev LogEvent) {
	if len(b.recent) < recentRing {
		b.recent = append(b.recent, ev)
		return
	}
	b.recent[b.wpos] = ev
	b.wpos = (b.wpos + 1) % recentRing
}

// snapAfter returns events with seq > afterSeq in chronological order.
// Caller must hold b.mu.
func (b *Broadcaster) snapAfter(afterSeq int64) []LogEvent {
	n := len(b.recent)
	all := make([]LogEvent, n)
	if n < recentRing {
		copy(all, b.recent)
	} else {
		copy(all, b.recent[b.wpos:])
		copy(all[recentRing-b.wpos:], b.recent[:b.wpos])
	}
	if afterSeq == 0 {
		return all
	}
	for i, ev := range all {
		if ev.Seq > afterSeq {
			return all[i:]
		}
	}
	return nil
}
