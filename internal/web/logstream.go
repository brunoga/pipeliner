package web

import (
	"strings"
	"sync"
)

const logBufCap = 10000

// Broadcaster fans log lines out to SSE subscribers and maintains a ring
// buffer so new clients can replay recent output on connect.
type Broadcaster struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
	lines   []string // ring buffer, capacity logBufCap
	wpos    int      // next write index when ring is full
}

// NewBroadcaster returns an empty Broadcaster ready for use.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan string]struct{}),
		lines:   make([]string, 0, logBufCap),
	}
}

// Write implements [io.Writer].
// slog.TextHandler calls Write once per record, so each call is one log line.
func (b *Broadcaster) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n\r")
	if line == "" {
		return len(p), nil
	}
	b.mu.Lock()
	b.appendLine(line)
	for ch := range b.clients {
		select {
		case ch <- line:
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

// Subscribe atomically snapshots the current buffer and registers a live
// channel, so no lines are missed between the two.
func (b *Broadcaster) Subscribe() (snapshot []string, live chan string) {
	live = make(chan string, 64)
	b.mu.Lock()
	snapshot = b.snap()
	b.clients[live] = struct{}{}
	b.mu.Unlock()
	return
}

// Unsubscribe removes a previously subscribed channel.
func (b *Broadcaster) Unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *Broadcaster) appendLine(line string) {
	if len(b.lines) < logBufCap {
		b.lines = append(b.lines, line)
	} else {
		b.lines[b.wpos] = line
		b.wpos = (b.wpos + 1) % logBufCap
	}
}

// snap returns a copy of the buffer in chronological order (oldest first).
// Must be called with b.mu held.
func (b *Broadcaster) snap() []string {
	n := len(b.lines)
	out := make([]string, n)
	if n < logBufCap {
		copy(out, b.lines)
	} else {
		copy(out, b.lines[b.wpos:])
		copy(out[logBufCap-b.wpos:], b.lines[:b.wpos])
	}
	return out
}
