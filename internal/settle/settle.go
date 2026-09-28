// Package settle implements the settle window shared by the movies and
// series filters: the delay between first seeing a download-worthy release
// for an item and actually grabbing one.
//
// Releases arrive in waves — 1080p, then 2160p, then an HDR pass, then an
// Atmos remux, often within hours — and grabbing on sight downloads every
// rung of that ladder. Delaying each *release* by a fixed age does not help:
// it shifts every rung later by the same amount. The window is therefore
// anchored to the item.
//
// Waiting alone is not enough, because source feeds are shallow: indexers
// commonly return only their newest ~50 items, a few hours' worth, so by the
// time a long window elapses the whole wave may have scrolled out of the feed
// and there would be nothing left to accept. The tracker therefore records
// the best candidate seen so far, and the caller downloads that remembered
// release when the window expires — whether or not it is still being
// advertised.
package settle

import (
	"strings"
	"time"

	"github.com/brunoga/pipeliner/quality"
)

// bucket is the minimal key-value interface the tracker requires.
type bucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
	Delete(key string) error
	Keys() ([]string, error)
}

// Candidate is a release being offered to a settle window.
type Candidate struct {
	URL     string          `json:"url"`
	Title   string          `json:"title"`
	Quality quality.Quality `json:"quality"`
	// Fields is the entry's field map, kept so the winner can be rebuilt and
	// downloaded after it has left the feed.
	Fields map[string]any `json:"fields,omitempty"`
}

// Record is the stored state of one item's settle window.
type Record struct {
	FirstSeen time.Time `json:"first_seen"`
	// Candidates is every download-worthy release seen during the window, in
	// arrival order, deduplicated by release name.
	//
	// The window keeps the whole wave rather than a running best because
	// picking a winner here means picking it on quality *tags*, before the
	// nodes that can actually veto a release have run — bitrate needs a
	// runtime from enrichment, a language condition needs metadata. A single
	// remembered winner therefore had to be re-chosen whenever a later gate
	// refused it, one settle window at a time. Emitting the wave lets the
	// pipeline's own gates thin it and dedup pick the best survivor, in one
	// pass, with no special case.
	//
	// This requires dedup to run *after* those gates; see the plugin READMEs.
	Candidates []Candidate `json:"candidates,omitempty"`
	// Best is the single-winner field written before the window kept the
	// whole wave. Records already on disk still carry it, so it is read and
	// folded into Candidates on first touch and never written again.
	Best *Candidate `json:"best,omitempty"`
}

// maxCandidates caps the wave stored per item. A real wave is a handful of
// releases; the cap only has to survive a pathological feed.
const maxCandidates = 24

// migrate folds a pre-wave record's single winner into Candidates. Returns
// true when the record changed and should be written back.
func (r *Record) migrate() bool {
	if r.Best == nil {
		return false
	}
	if len(r.Candidates) == 0 && r.Best.URL != "" {
		r.Candidates = []Candidate{*r.Best}
	}
	r.Best = nil
	return true
}

// has reports whether a release name is already recorded as a candidate.
func (r *Record) has(title string) bool {
	norm := NormalizeTitle(title)
	for _, c := range r.Candidates {
		if NormalizeTitle(c.Title) == norm {
			return true
		}
	}
	return false
}

// NormalizeTitle is how two release names are compared for identity, when
// deciding whether a recorded release is still being advertised.
//
// The release name is the only stable handle a release has here. Its URL is
// re-encrypted by the indexer on every search, and the settle key identifies
// the *item* rather than the release — so a key comparison cannot tell the
// recorded winner apart from a worse sibling of the same film.
func NormalizeTitle(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Tracker persists settle windows in a bucket.
type Tracker struct{ b bucket }

// New returns a tracker backed by the given bucket.
func New(b bucket) *Tracker { return &Tracker{b: b} }

// Offer records a download-worthy candidate for key and reports how much of
// the settle window remains. A non-positive result means the window has
// elapsed and the caller should release the wave now.
//
// Every distinct release is kept, so when the window closes the caller can
// hand the whole wave to the pipeline and let the gates and dedup decide.
func (t *Tracker) Offer(key string, c Candidate, window time.Duration, now time.Time) time.Duration {
	if t == nil || t.b == nil || window <= 0 {
		return 0
	}
	var rec Record
	found, err := t.b.Get(key, &rec)
	if err != nil || !found || rec.FirstSeen.IsZero() {
		_ = t.b.Put(key, Record{FirstSeen: now, Candidates: []Candidate{c}})
		return window
	}
	changed := rec.migrate()
	if !rec.has(c.Title) {
		rec.Candidates = append(rec.Candidates, c)
		if len(rec.Candidates) > maxCandidates {
			// Drop the oldest: a wave that overflows this is a churning feed,
			// and the newest releases are the ones worth keeping.
			rec.Candidates = rec.Candidates[len(rec.Candidates)-maxCandidates:]
		}
		changed = true
	}
	if changed {
		_ = t.b.Put(key, rec)
	}
	if elapsed := now.Sub(rec.FirstSeen); elapsed < window {
		return window - elapsed
	}
	return 0
}

// Best returns the highest-quality candidate recorded for key. The window no
// longer decides on quality alone — the pipeline does — but the best-tagged
// release is still the useful thing to report when describing a window.
func (t *Tracker) Best(key string) (Candidate, bool) {
	cands := t.Candidates(key)
	if len(cands) == 0 {
		return Candidate{}, false
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.Quality.Better(best.Quality) {
			best = c
		}
	}
	return best, true
}

// Candidates returns every release recorded for key, in arrival order.
func (t *Tracker) Candidates(key string) []Candidate {
	if t == nil || t.b == nil {
		return nil
	}
	var rec Record
	found, err := t.b.Get(key, &rec)
	if err != nil || !found {
		return nil
	}
	rec.migrate()
	return rec.Candidates
}

// KeyedCandidate pairs a stored key with one of its recorded releases.
type KeyedCandidate struct {
	Key     string
	Release Candidate
}

// Expired lists every recorded release of every window that has elapsed,
// restricted to the keys owned by task. One window contributes as many
// entries as it has candidates; the caller hands them all to the pipeline,
// whose gates thin them and whose dedup picks the survivor.
//
// The task restriction is load-bearing, not hygiene: a release is revived by
// injecting it straight into the caller's filter, skipping every upstream
// node, so returning another pipeline's pending release would smuggle it
// past gates it never passed.
func (t *Tracker) Expired(task string, window time.Duration, now time.Time) []KeyedCandidate {
	if t == nil || t.b == nil || window <= 0 || task == "" {
		return nil
	}
	keys, err := t.b.Keys()
	if err != nil {
		return nil
	}
	prefix := TaskPrefix(task)
	var out []KeyedCandidate
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		var rec Record
		found, err := t.b.Get(k, &rec)
		if err != nil || !found || rec.FirstSeen.IsZero() {
			continue
		}
		rec.migrate()
		if now.Sub(rec.FirstSeen) < window {
			continue
		}
		for _, c := range rec.Candidates {
			if c.URL == "" {
				continue
			}
			out = append(out, KeyedCandidate{Key: k, Release: c})
		}
	}
	return out
}

// Clear ends the window for key, so the next wave starts a fresh timer.
func (t *Tracker) Clear(key string) {
	if t == nil || t.b == nil {
		return
	}
	_ = t.b.Delete(key)
}
