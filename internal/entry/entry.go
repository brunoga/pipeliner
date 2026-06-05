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

// StateSet is a bitset over the four entry States. Used by
// plugin.Descriptor.InputStates to declare which states a plugin's
// Process/Consume method acts on; the executor pre-filters upstream entries
// to the declared set before calling the plugin. This removes the
// per-plugin "skip rejected/failed" boilerplate that used to live at the top
// of every Process method.
//
// Use the named constants below in plugin descriptors rather than assembling
// bitsets by hand.
type StateSet uint8

// StateBit returns the StateSet containing only s. Useful for assembling
// custom sets, but most plugins should use the pre-built named constants.
// Returns 0 for an out-of-range State, which is the empty set.
func StateBit(s State) StateSet {
	switch s {
	case Undecided:
		return 1 << 0
	case Accepted:
		return 1 << 1
	case Rejected:
		return 1 << 2
	case Failed:
		return 1 << 3
	}
	return 0
}

// Pre-built sets covering every common case. A plugin descriptor's
// InputStates field defaults (via Descriptor.EffectiveInputStates) to a
// role-appropriate value when left unset:
//
//   - RoleProcessor → StatesAcceptedUndecided
//   - RoleSink      → handled separately by FilterAccepted (consumed-aware)
//
// Plugins with non-default needs declare an explicit set:
//
//   - swap_state                       → StatesAll
//   - accept_all                       → StatesUndecidedOnly
//   - dedup, limit, discover           → StatesAcceptedOnly
var (
	StatesAcceptedOnly      = StateBit(Accepted)
	StatesUndecidedOnly     = StateBit(Undecided)
	StatesAcceptedUndecided = StateBit(Accepted) | StateBit(Undecided)
	StatesAllButFailed      = StateBit(Accepted) | StateBit(Rejected) | StateBit(Undecided)
	StatesAll               = StateBit(Accepted) | StateBit(Rejected) | StateBit(Failed) | StateBit(Undecided)
)

// Has reports whether s is present in the set.
func (ss StateSet) Has(s State) bool { return ss&StateBit(s) != 0 }

// SplitConsumed partitions entries into (nonConsumed, consumed) preserving
// original order in each output slice. The executor uses this at the sink
// boundary in addition to the InputStates pre-filter: a sink that handled an
// entry by other means (e.Consume()) must still skip chained sinks even when
// the entry's State remains Accepted.
//
// `consumed` is orthogonal to State — it stays on the entry across state
// transitions and survives a swap_state flip. Both output slices reference
// the same *Entry pointers as input; no clone. Either slice may be nil when
// no entries fall on that side.
func SplitConsumed(entries []*Entry) (nonConsumed, consumed []*Entry) {
	for _, e := range entries {
		if e.consumed {
			consumed = append(consumed, e)
		} else {
			nonConsumed = append(nonConsumed, e)
		}
	}
	return
}

// SplitByStates partitions entries into (matching, nonMatching) preserving
// original order in each output slice. Used by the executor to pre-filter
// upstream entries to a plugin's declared InputStates while keeping the
// excluded entries available to forward downstream unchanged.
//
// Both output slices reference the same *Entry pointers as input — no clone.
// Either slice may be nil when no entries fall on that side.
func SplitByStates(entries []*Entry, states StateSet) (matching, nonMatching []*Entry) {
	for _, e := range entries {
		if states.Has(e.State) {
			matching = append(matching, e)
		} else {
			nonMatching = append(nonMatching, e)
		}
	}
	return
}

// Entry is the core data unit that flows through a pipeline task.
type Entry struct {
	Title        string
	URL          string
	OriginalURL  string // set once at input time, never mutated by plugins
	State        State
	AcceptReason string
	RejectReason string
	FailReason   string
	Task         string // owning task name, set by the engine
	Fields       map[string]any

	// LastStateChange records the most recent programmatic state transition
	// caused by a state-mutating plugin (e.g. swap_state). nil when no such
	// transition has occurred. AcceptReason / RejectReason / FailReason are
	// preserved across transitions as audit history, so a notify template can
	// render both the original cause and the reason for the override:
	//
	//   {{.FailReason}}                     "deluge: connection refused"
	//   {{.LastStateChange.Reason}}         "swap_state: swapped accepted ↔ failed"
	LastStateChange *StateChange

	// consumed is set by Consume(). It keeps State = Accepted (so CommitPlugin
	// still runs for this entry) but signals FilterAccepted to exclude it from
	// subsequent sinks. Use it when the side effect was already applied by other
	// means and chained notification sinks should be silent.
	consumed bool
}

// StateChange records a programmatic transition between entry states caused
// by a plugin that explicitly mutates state (currently only swap_state). It
// is orthogonal to AcceptReason / RejectReason / FailReason — those carry the
// reason an entry first entered a state and are preserved across transitions
// as audit history; LastStateChange carries the reason the state was
// subsequently overridden.
type StateChange struct {
	From   State
	To     State
	Plugin string // plugin name that caused the change (e.g. "swap_state")
	Reason string // human-readable description of the cause
	At     time.Time
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
// An optional reason string is stored in AcceptReason for logging.
func (e *Entry) Accept(reason ...string) {
	if e.State != Rejected {
		e.State = Accepted
		if len(reason) > 0 {
			e.AcceptReason = reason[0]
		}
	}
}

// Reject moves the entry to Rejected regardless of current state. Reject always wins.
func (e *Entry) Reject(reason string) {
	e.State = Rejected
	e.RejectReason = reason
}

// Fail marks the entry as Failed with the given reason.
// No-op if the entry is already Rejected (rejection always wins).
func (e *Entry) Fail(reason string) {
	if e.State != Rejected {
		e.State = Failed
		e.FailReason = reason
	}
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

// Delete removes a field from the entry's Fields map. No-op if absent.
func (e *Entry) Delete(key string) {
	delete(e.Fields, key)
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

// Get retrieves a value by name. Fields takes precedence so plugins that
// explicitly populate a key (e.g. metainfo plugins setting "title") win.
// If the key is one of the well-known struct field names — "title",
// "url", "original_url", "task" — Get falls back to the struct field
// when Fields has nothing for that key.
//
// This mirrors the convention [interp.EntryData] already uses for
// templates and expressions, so name-based consumers (require,
// condition rules, route ports, the typed Get* helpers below) all see
// the same logical view of the entry: every standard field is
// addressable by its lowercase name regardless of whether the value
// lives in the Fields bag or on the Entry struct.
//
// The second return value reports whether a value was found. For
// struct fields it is always true — the field always exists on the
// entry, even when its value is the zero string; callers that care
// about emptiness should test the value itself (which is what
// [require] does via its isMissing helper).
func (e *Entry) Get(key string) (any, bool) {
	if v, ok := e.Fields[key]; ok {
		return v, true
	}
	switch key {
	case "title":
		return e.Title, true
	case "url":
		return e.URL, true
	case "original_url":
		return e.OriginalURL, true
	case "task":
		return e.Task, true
	}
	return nil, false
}

// GetString returns the string value for key, or "" if absent or wrong type.
// Honours the struct-field fallback documented on [Entry.Get].
func (e *Entry) GetString(key string) string {
	v, ok := e.Get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// GetInt returns the int value for key, or 0 if absent or wrong type.
// Honours the struct-field fallback documented on [Entry.Get] (though
// none of the fallback fields are integers, the indirection keeps the
// accessors consistent).
func (e *Entry) GetInt(key string) int {
	v, ok := e.Get(key)
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
// Honours the struct-field fallback documented on [Entry.Get].
func (e *Entry) GetBool(key string) bool {
	v, ok := e.Get(key)
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// GetTime returns the time.Time value for key, or zero time if absent or wrong type.
// Honours the struct-field fallback documented on [Entry.Get].
func (e *Entry) GetTime(key string) time.Time {
	v, ok := e.Get(key)
	if !ok {
		return time.Time{}
	}
	t, _ := v.(time.Time)
	return t
}

// Clone returns a deep copy of the entry. Mutating the clone does not affect the original.
func (e *Entry) Clone() *Entry {
	clone := &Entry{
		Title:        e.Title,
		URL:          e.URL,
		OriginalURL:  e.OriginalURL,
		State:        e.State,
		AcceptReason: e.AcceptReason,
		RejectReason: e.RejectReason,
		FailReason:   e.FailReason,
		Task:         e.Task,
		Fields:       maps.Clone(e.Fields),
		consumed:     e.consumed,
	}
	if e.LastStateChange != nil {
		sc := *e.LastStateChange
		clone.LastStateChange = &sc
	}
	return clone
}

func (e *Entry) String() string {
	return fmt.Sprintf("Entry{title=%q url=%q state=%s}", e.Title, e.URL, e.State)
}
