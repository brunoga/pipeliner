// Package pipelinestate manages the state a pipeline accumulates under its own
// name, so renaming a pipeline or folding two together does not silently throw
// it away.
//
// Several plugins namespace their records by pipeline name: a `seen` node with
// local=True writes to "seen:<pipeline>", a `movies` filter with local=True to
// "movies:<pipeline>", and `discover` and `series_gaps` always scope to their
// pipeline. The name is therefore load-bearing, and the store has no idea that
// a name in a config became a different name in a later config: renaming a
// pipeline presents it with an empty bucket, which reads as "nothing has ever
// been downloaded" and re-grabs everything. Deleting a pipeline leaves its
// buckets behind forever, invisible to everything but the database tab.
//
// Nothing here is automatic, and that is the point: a config that lost
// "movies-3d-discover" and gained "movies-3d-all" is indistinguishable from
// one that deleted the first and created the second, so guessing would
// sometimes merge state that was meant to stay separate. Instead the orphaned
// state is reported (see Orphans) and moved or dropped by an explicit
// operation.
package pipelinestate

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/store"
)

// Store is the part of the store this package needs.
type Store interface {
	Buckets() (map[string]int, error)
	Bucket(name string) store.Bucket
}

// Kind is one family of per-pipeline state: a bucket prefix plus how two of
// them combine.
//
// To add a per-pipeline bucket, register it here. A bucket that is namespaced
// by pipeline name and missing from this list is state that a rename loses
// without a word, which is the whole failure this package exists to prevent.
type Kind struct {
	// Prefix is the bucket name before the ":<pipeline>" suffix.
	Prefix string
	// Label describes the records for a human reading a report.
	Label string
	// Merge resolves a key that exists on both sides, returning the record to
	// keep. A nil Merge marks a bucket that holds one indivisible piece of
	// state rather than a set of independent records -- it can be chosen
	// between, never combined.
	Merge func(dst, src []byte) ([]byte, error)
	// Retag rewrites a record that carries a denormalised copy of its own
	// pipeline name, so a moved record does not keep naming the pipeline it
	// came from. Nil when records do not name their pipeline.
	Retag func(raw []byte, pipeline string) ([]byte, error)
}

// Mergeable reports whether two of these buckets can be combined.
func (k Kind) Mergeable() bool { return k.Merge != nil }

// kinds is the registry. Each policy follows from what the records are, which
// is recorded here because the reasoning is not recoverable from the code.
var kinds = []Kind{
	{
		Prefix: "seen",
		Label:  "seen records",
		// Keys are content fingerprints, so a key on both sides is the same
		// release seen twice and the two records say the same thing. The
		// destination's copy stands; only a key's absence has any effect,
		// which is why union is the whole of the merge.
		Merge: keepDest,
		// The record repeats its pipeline name in a "task" field. It is not
		// read by anything, but it is shown verbatim in the database tab, so
		// leaving it stale would make the tab lie about where a record lives.
		Retag: retagField("task"),
	},
	{
		Prefix: imovies.TrackerBucketName,
		Label:  "movie tracker records",
		// A record states the quality of the copy that was downloaded, keyed
		// by title+year+3d. When both sides hold the same film, the better
		// copy is the true answer to "what do I have": keeping the worse one
		// would invite a downgrade or a re-grab of something already owned.
		Merge: keepBetterQuality,
	},
	{
		Prefix: "discover",
		Label:  "discover search cache",
		// A record is "I searched for this title at T, and these came back".
		// Both sides are equally valid, so the newer one is simply the more
		// recent truth -- and keeping it avoids re-running a search that has
		// already been paid for.
		Merge: keepNewer("last_searched"),
	},
	{
		Prefix: "series_gaps",
		Label:  "gap-scan cursor",
		// Not a set of records: a "cursor" key holding one scan position plus
		// a "pending" map. Two scan positions have no meaningful union -- a
		// merged cursor would be a position neither pipeline was at -- so this
		// bucket is chosen between, never combined. Merge stays nil.
	},
}

// Kinds returns the registered kinds.
func Kinds() []Kind { return slices.Clone(kinds) }

// kindOf splits a bucket name into its kind and pipeline name.
func kindOf(bucket string) (Kind, string, bool) {
	for _, k := range kinds {
		if pipeline, ok := strings.CutPrefix(bucket, k.Prefix+":"); ok && pipeline != "" {
			return k, pipeline, true
		}
	}
	return Kind{}, "", false
}

// BucketState is one bucket belonging to one pipeline.
type BucketState struct {
	Kind   Kind   `json:"-"`
	Bucket string `json:"bucket"`
	Label  string `json:"label"`
	Rows   int    `json:"rows"`
}

// State is everything one pipeline has in the store.
type State struct {
	Pipeline string        `json:"pipeline"`
	Buckets  []BucketState `json:"buckets"`
	// Orphaned is true when no pipeline of this name exists in the config.
	Orphaned bool `json:"orphaned"`
}

// Rows is the total number of records across this pipeline's buckets.
func (s State) Rows() int {
	n := 0
	for _, b := range s.Buckets {
		n += b.Rows
	}
	return n
}

// Scan groups every per-pipeline bucket in the store by pipeline name. Passing
// the live pipeline names marks the ones no config claims; pass nil to mark
// none.
func Scan(db Store, live []string) ([]State, error) {
	counts, err := db.Buckets()
	if err != nil {
		return nil, err
	}
	byPipeline := map[string]*State{}
	for bucket, rows := range counts {
		k, pipeline, ok := kindOf(bucket)
		if !ok {
			continue
		}
		st := byPipeline[pipeline]
		if st == nil {
			st = &State{Pipeline: pipeline, Orphaned: live != nil && !slices.Contains(live, pipeline)}
			byPipeline[pipeline] = st
		}
		st.Buckets = append(st.Buckets, BucketState{Kind: k, Bucket: bucket, Label: k.Label, Rows: rows})
	}
	out := make([]State, 0, len(byPipeline))
	for _, st := range byPipeline {
		sort.Slice(st.Buckets, func(i, j int) bool { return st.Buckets[i].Bucket < st.Buckets[j].Bucket })
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pipeline < out[j].Pipeline })
	return out, nil
}

// Orphans returns the state whose pipeline is not in live. An empty live list
// means every pipeline is gone, which is what an empty config describes; a nil
// live list is "do not judge" and returns nothing.
func Orphans(db Store, live []string) ([]State, error) {
	if live == nil {
		return nil, nil
	}
	all, err := Scan(db, live)
	if err != nil {
		return nil, err
	}
	var out []State
	for _, st := range all {
		if st.Orphaned {
			out = append(out, st)
		}
	}
	return out, nil
}

// Side names which bucket wins where a choice cannot be computed.
type Side string

const (
	// PreferUnset makes an unmergeable collision an error rather than a guess.
	PreferUnset Side = ""
	// PreferSource keeps the state being moved.
	PreferSource Side = "source"
	// PreferDest keeps the state already at the destination.
	PreferDest Side = "dest"
)

// MoveOptions tunes Move.
type MoveOptions struct {
	// Prefer resolves a collision in a bucket that cannot be merged. Required
	// only when both sides hold such a bucket.
	Prefer Side
	// DryRun computes the report and writes nothing.
	DryRun bool
}

// Step is what happened to one bucket.
type Step struct {
	Bucket string `json:"bucket"`
	Label  string `json:"label"`
	// Moved is the number of records copied to a key the destination did not
	// have.
	Moved int `json:"moved"`
	// Conflicts is the number of keys present on both sides.
	Conflicts int `json:"conflicts"`
	// SourceWon counts the conflicts the source's record settled.
	SourceWon int `json:"source_won"`
	// Deleted is the number of source records removed afterwards.
	Deleted int `json:"deleted"`
	// Note explains an outcome a count cannot, e.g. a cursor that was chosen
	// rather than merged.
	Note string `json:"note,omitempty"`
}

// Report is the outcome of an operation, suitable for printing or returning
// over the API. It is filled in for a dry run too.
type Report struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	DryRun bool   `json:"dry_run"`
	Steps  []Step `json:"steps"`
}

// Total sums one field across the steps.
func (r *Report) Total(f func(Step) int) int {
	n := 0
	for _, s := range r.Steps {
		n += f(s)
	}
	return n
}

// ErrNeedPrefer is returned when a bucket that cannot be merged exists on both
// sides and no preference was given.
var ErrNeedPrefer = errors.New("pipelinestate: collision in a bucket that cannot be merged")

// Move transfers every per-pipeline bucket from one pipeline name to another.
//
// With nothing at the destination this is a rename. With state on both sides it
// is an adoption -- what you want when two pipelines become one -- and each
// bucket combines by its kind's rule. A bucket that cannot be merged (a scan
// cursor) and exists on both sides needs an explicit Prefer, because picking
// one silently would move a scan to a position its pipeline was never at.
func Move(db Store, from, to string, opt MoveOptions) (*Report, error) {
	// Planned first, applied second. A move touches several buckets and the
	// store has no transaction to wrap them in, so an error discovered at the
	// third bucket would otherwise leave the first two already moved -- a
	// half-renamed pipeline, which is worse than either outcome. The planning
	// pass decodes and merges every record without writing, so by the time
	// anything is written the only errors left are the store's own.
	plan, err := move(db, from, to, opt, false)
	if err != nil || opt.DryRun {
		return plan, err
	}
	return move(db, from, to, opt, true)
}

func move(db Store, from, to string, opt MoveOptions, apply bool) (*Report, error) {
	if from == "" || to == "" {
		return nil, errors.New("pipelinestate: both a source and a destination name are required")
	}
	if from == to {
		return nil, errors.New("pipelinestate: source and destination are the same pipeline")
	}
	counts, err := db.Buckets()
	if err != nil {
		return nil, err
	}
	rep := &Report{From: from, To: to, DryRun: opt.DryRun}

	for _, k := range kinds {
		srcName, dstName := k.Prefix+":"+from, k.Prefix+":"+to
		if counts[srcName] == 0 {
			continue
		}
		step := Step{Bucket: srcName, Label: k.Label}
		src, dst := db.Bucket(srcName), db.Bucket(dstName)
		srcRows, err := src.All()
		if err != nil {
			return nil, err
		}

		if !k.Mergeable() && counts[dstName] > 0 {
			switch opt.Prefer {
			case PreferSource:
				step.Note = fmt.Sprintf("replaced the destination's %s (%d records) -- not merged, chosen", k.Label, counts[dstName])
				step.Moved, step.Conflicts = len(srcRows), counts[dstName]
				if apply {
					if err := clear(dst); err != nil {
						return nil, err
					}
					if err := copyInto(dst, srcRows, k, to); err != nil {
						return nil, err
					}
				}
			case PreferDest:
				step.Note = fmt.Sprintf("kept the destination's %s -- not merged, chosen", k.Label)
				step.Conflicts = len(srcRows)
			default:
				return nil, fmt.Errorf("%w: %q holds a %s and %q has one too; choose which to keep",
					ErrNeedPrefer, srcName, k.Label, dstName)
			}
			if apply {
				if err := clear(src); err != nil {
					return nil, err
				}
			}
			step.Deleted = len(srcRows)
			rep.Steps = append(rep.Steps, step)
			continue
		}

		dstRows, err := dst.All()
		if err != nil {
			return nil, err
		}
		for key, srcRaw := range srcRows {
			dstRaw, collides := dstRows[key]
			raw := srcRaw
			if collides {
				step.Conflicts++
				// A kind with no Merge only reaches here with an empty
				// destination, so Merge is safe to call.
				merged, err := k.Merge(dstRaw, srcRaw)
				if err != nil {
					return nil, fmt.Errorf("pipelinestate: merge %q/%q: %w", srcName, key, err)
				}
				if !equalJSON(merged, dstRaw) {
					step.SourceWon++
				}
				raw = merged
			} else {
				step.Moved++
			}
			if !apply {
				continue
			}
			if err := putRetagged(dst, key, raw, k, to); err != nil {
				return nil, err
			}
		}
		step.Deleted = len(srcRows)
		if apply {
			if err := clear(src); err != nil {
				return nil, err
			}
		}
		rep.Steps = append(rep.Steps, step)
	}
	return rep, nil
}

// Forget deletes every per-pipeline bucket belonging to a pipeline name. It is
// the deliberate end of state whose pipeline is gone; there is no undo beyond
// a database backup, which is why the callers all confirm first.
func Forget(db Store, name string, dryRun bool) (*Report, error) {
	if name == "" {
		return nil, errors.New("pipelinestate: a pipeline name is required")
	}
	counts, err := db.Buckets()
	if err != nil {
		return nil, err
	}
	rep := &Report{From: name, DryRun: dryRun}
	for _, k := range kinds {
		bucket := k.Prefix + ":" + name
		if counts[bucket] == 0 {
			continue
		}
		rep.Steps = append(rep.Steps, Step{
			Bucket: bucket, Label: k.Label, Deleted: counts[bucket],
		})
		if dryRun {
			continue
		}
		if err := clear(db.Bucket(bucket)); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// --- helpers ---

func clear(b store.Bucket) error {
	keys, err := b.Keys()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

func copyInto(dst store.Bucket, rows map[string][]byte, k Kind, pipeline string) error {
	for key, raw := range rows {
		if err := putRetagged(dst, key, raw, k, pipeline); err != nil {
			return err
		}
	}
	return nil
}

func putRetagged(dst store.Bucket, key string, raw []byte, k Kind, pipeline string) error {
	if k.Retag != nil {
		out, err := k.Retag(raw, pipeline)
		if err != nil {
			return fmt.Errorf("pipelinestate: retag %q: %w", key, err)
		}
		raw = out
	}
	return dst.Put(key, json.RawMessage(raw))
}

func equalJSON(a, b []byte) bool { return string(a) == string(b) }

// keepDest resolves a collision by leaving the destination untouched.
func keepDest(dst, _ []byte) ([]byte, error) { return dst, nil }

// keepBetterQuality keeps whichever movie record describes the better copy,
// and the destination's on a tie.
func keepBetterQuality(dst, src []byte) ([]byte, error) {
	var d, s imovies.Record
	if err := json.Unmarshal(dst, &d); err != nil {
		return nil, fmt.Errorf("destination record: %w", err)
	}
	if err := json.Unmarshal(src, &s); err != nil {
		return nil, fmt.Errorf("source record: %w", err)
	}
	if s.Quality.Better(d.Quality) {
		return src, nil
	}
	return dst, nil
}

// keepNewer keeps whichever record carries the later timestamp in field, and
// the destination's when neither is later (including when both lack the field,
// which makes the choice arbitrary but not wrong -- the records are caches).
func keepNewer(field string) func(dst, src []byte) ([]byte, error) {
	return func(dst, src []byte) ([]byte, error) {
		dt, err := jsonTime(dst, field)
		if err != nil {
			return nil, fmt.Errorf("destination record: %w", err)
		}
		st, err := jsonTime(src, field)
		if err != nil {
			return nil, fmt.Errorf("source record: %w", err)
		}
		if st.After(dt) {
			return src, nil
		}
		return dst, nil
	}
}

func jsonTime(raw []byte, field string) (time.Time, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return time.Time{}, err
	}
	v, ok := obj[field]
	if !ok {
		return time.Time{}, nil
	}
	var t time.Time
	if err := json.Unmarshal(v, &t); err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", field, err)
	}
	return t, nil
}

// retagField rewrites one string field, leaving every other field byte for
// byte as it was -- a record may hold fields this build does not know about,
// and a decode-and-re-encode round trip through a struct would drop them.
func retagField(field string) func(raw []byte, pipeline string) ([]byte, error) {
	return func(raw []byte, pipeline string) ([]byte, error) {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		if _, ok := obj[field]; !ok {
			return raw, nil
		}
		v, err := json.Marshal(pipeline)
		if err != nil {
			return nil, err
		}
		obj[field] = v
		return json.Marshal(obj)
	}
}
