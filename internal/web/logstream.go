package web

import (
	"strings"
	"sync"
)

const logBufCap = 10000

// logLine pairs a sequence number with a log text line.
type logLine struct {
	seq  int64
	text string
}

// Broadcaster fans log lines out to SSE subscribers and maintains a ring
// buffer so new clients can replay recent output on connect.
// Each line is assigned a monotonically increasing sequence number used
// as the SSE event id, so reconnecting clients can resume from where they
// left off via the Last-Event-ID header rather than replaying the full buffer.
type Broadcaster struct {
	mu      sync.Mutex
	clients map[chan logLine]struct{}
	lines   []logLine // ring buffer, capacity logBufCap
	wpos    int       // next write index when ring is full
	seq     int64     // monotonically increasing sequence counter
}

// NewBroadcaster returns an empty Broadcaster ready for use.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan logLine]struct{}),
		lines:   make([]logLine, 0, logBufCap),
	}
}

// Write implements [io.Writer].
// slog.TextHandler calls Write once per record, so each call is one log line.
func (b *Broadcaster) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\n\r")
	if text == "" {
		return len(p), nil
	}
	b.mu.Lock()
	b.seq++
	ll := logLine{seq: b.seq, text: text}
	b.appendLine(ll)
	for ch := range b.clients {
		select {
		case ch <- ll:
		default:
		}
	}
	b.mu.Unlock()
	return len(p), nil
}

// Reset clears the log buffer. Call at the start of a new run batch so
// clients that connect later see only the current run's output.
func (b *Broadcaster) Reset() {
	b.mu.Lock()
	b.lines = b.lines[:0]
	b.wpos = 0
	b.mu.Unlock()
}

// Subscribe atomically snapshots lines with seq > afterSeq and registers a
// live channel. Pass afterSeq=0 to receive the full buffer.
// No lines are missed between the snapshot and live delivery.
func (b *Broadcaster) Subscribe(afterSeq int64) (snapshot []logLine, live chan logLine) {
	live = make(chan logLine, 64)
	b.mu.Lock()
	snapshot = b.snapAfter(afterSeq)
	b.clients[live] = struct{}{}
	b.mu.Unlock()
	return
}

// Unsubscribe removes a previously subscribed channel.
func (b *Broadcaster) Unsubscribe(ch chan logLine) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *Broadcaster) appendLine(ll logLine) {
	if len(b.lines) < logBufCap {
		b.lines = append(b.lines, ll)
	} else {
		b.lines[b.wpos] = ll
		b.wpos = (b.wpos + 1) % logBufCap
	}
}

// snapAfter returns lines with seq > afterSeq in chronological order.
// Must be called with b.mu held.
func (b *Broadcaster) snapAfter(afterSeq int64) []logLine {
	n := len(b.lines)
	all := make([]logLine, n)
	if n < logBufCap {
		copy(all, b.lines)
	} else {
		copy(all, b.lines[b.wpos:])
		copy(all[logBufCap-b.wpos:], b.lines[:b.wpos])
	}
	if afterSeq == 0 {
		return all
	}
	for i, ll := range all {
		if ll.seq > afterSeq {
			return all[i:]
		}
	}
	return nil
}
