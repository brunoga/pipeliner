// Package entry defines the core data unit that flows through the pipeline.
package entry

import (
	"fmt"
	"maps"
	"time"
)

// State represents the decision state of an Entry as it flows through the pipeline.
type State int

const (
	Undecided State = iota
	Accepted
	Rejected
	Failed
)

func (s State) String() string {
	switch s {
	case Undecided:
		return "undecided"
	case Accepted:
		return "accepted"
	case Rejected:
		return "rejected"
	case Failed:
		return "failed"
	default:
		return "unknown"
	}
}

// Entry is the core data unit that flows through a pipeline task.
type Entry struct {
	Title        string
	URL          string
	OriginalURL  string // set once at input time, never mutated by plugins
	State        State
	RejectReason string
	FailReason   string
	Task         string // owning task name, set by the engine
	Fields       map[string]any

	// consumed is set by Consume(). It keeps State = Accepted (so CommitPlugin
	// still runs for this entry) but signals FilterAccepted to exclude it from
	// subsequent sinks. Use it when the side effect was already applied by other
	// means and chained notification sinks should be silent.
	consumed bool
}

// New creates an Undecided entry with the given title and URL.
func New(title, url string) *Entry {
	return &Entry{
		Title:       title,
		URL:         url,
		OriginalURL: url,
		State:       Undecided,
		Fields:      make(map[string]any),
	}
}

// Accept moves the entry to Accepted unless it is already Rejected.
func (e *Entry) Accept() {
	if e.State != Rejected {
		e.State = Accepted
	}
}

// Reject moves the entry to Rejected regardless of current state. Reject always wins.
func (e *Entry) Reject(reason string) {
	e.State = Rejected
	e.RejectReason = reason
}

// Fail marks the entry as Failed with the given reason.
func (e *Entry) Fail(reason string) {
	e.State = Failed
	e.FailReason = reason
}

func (e *Entry) IsAccepted() bool  { return e.State == Accepted }
func (e *Entry) IsRejected() bool  { return e.State == Rejected }
func (e *Entry) IsFailed() bool    { return e.State == Failed }
func (e *Entry) IsUndecided() bool { return e.State == Undecided }
func (e *Entry) IsConsumed() bool  { return e.consumed }

// Consume marks the entry as silently handled: subsequent sinks are skipped
// (same as Fail), but the entry remains Accepted so CommitPlugin.Commit still
// runs for it (unlike Fail). Use when the side effect was already applied by
// other means and chained notification sinks should be silent.
func (e *Entry) Consume() {
	if e.State != Rejected && e.State != Failed {
		e.consumed = true
	}
}

// Set stores a value in the entry's metadata bag.
func (e *Entry) Set(key string, value any) {
	e.Fields[key] = value
}

// FilterAccepted returns entries that are Accepted and not Consumed. Used by
// SinkPlugin implementations and the executor to pass entries to subsequent
// sinks. Consumed entries (marked by a prior sink via e.Consume()) are
// excluded so they do not trigger chained notification sinks, even though
// CommitPlugin.Commit still runs for them.
func FilterAccepted(entries []*Entry) []*Entry {
	out := make([]*Entry, 0, len(entries))
	for _, e := range entries {
		if e.IsAccepted() && !e.consumed {
			out = append(out, e)
		}
	}
	return out
}

// PassThrough returns entries that are not rejected or failed. Used by
// ProcessorPlugin implementations to filter their output slice.
// When all entries pass, the original slice is returned without allocation.
func PassThrough(entries []*Entry) []*Entry {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			out := make([]*Entry, 0, len(entries))
			for _, e2 := range entries {
				if !e2.IsRejected() && !e2.IsFailed() {
					out = append(out, e2)
				}
			}
			return out
		}
	}
	return entries
}

// Get retrieves a value from the entry's metadata bag.
func (e *Entry) Get(key string) (any, bool) {
	v, ok := e.Fields[key]
	return v, ok
}

// GetString returns the string value for key, or "" if absent or wrong type.
func (e *Entry) GetString(key string) string {
	v, ok := e.Fields[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// GetInt returns the int value for key, or 0 if absent or wrong type.
func (e *Entry) GetInt(key string) int {
	v, ok := e.Fields[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// GetBool returns the bool value for key, or false if absent or wrong type.
func (e *Entry) GetBool(key string) bool {
	v, ok := e.Fields[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// GetTime returns the time.Time value for key, or zero time if absent or wrong type.
func (e *Entry) GetTime(key string) time.Time {
	v, ok := e.Fields[key]
	if !ok {
		return time.Time{}
	}
	t, _ := v.(time.Time)
	return t
}

// Clone returns a deep copy of the entry. Mutating the clone does not affect the original.
func (e *Entry) Clone() *Entry {
	return &Entry{
		Title:        e.Title,
		URL:          e.URL,
		OriginalURL:  e.OriginalURL,
		State:        e.State,
		RejectReason: e.RejectReason,
		FailReason:   e.FailReason,
		Task:         e.Task,
		Fields:       maps.Clone(e.Fields),
		consumed:     e.consumed,
	}
}

func (e *Entry) String() string {
	return fmt.Sprintf("Entry{title=%q url=%q state=%s}", e.Title, e.URL, e.State)
}
