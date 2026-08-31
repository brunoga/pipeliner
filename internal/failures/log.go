// Package failures maintains an append-only audit log of entries that FAILED —
// entries whose side effect errored at a sink (a torrent client refusing the
// add, a download error, an exec command failing), as distinct from entries
// that were routinely rejected by a filter. Rejections are high-volume feed
// churn and are deliberately not logged; failures are rare and worth keeping.
//
// This is the durable counterpart to the run inspector's traces, which are
// bounded to the last few runs: the failure log answers "why did this entry
// fail last month?" long after the trace has been evicted.
package failures

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/executor"
)

// BucketName is the store bucket holding the failure audit log.
const BucketName = "failure_log"

// Record is one failed entry.
type Record struct {
	// Title is the release title as it failed.
	Title string `json:"title"`
	// URL is the entry URL.
	URL string `json:"url,omitempty"`
	// Reason is the failure reason (e.FailReason) — usually names the plugin
	// and the underlying error.
	Reason string `json:"reason,omitempty"`
	// Node is the graph node that failed the entry, when known (best-effort;
	// empty for entries beyond the trace cap).
	Node string `json:"node,omitempty"`
	// Task is the pipeline the failure occurred in.
	Task string `json:"task,omitempty"`
	// MediaType is the entry's media_type when classified upstream.
	MediaType string `json:"media_type,omitempty"`
	// FailedAt is when the run that produced the failure completed.
	FailedAt time.Time `json:"failed_at"`
}

// bucket is the minimal store interface the log needs.
type bucket interface {
	Put(key string, value any) error
	All() (map[string][]byte, error)
}

// Log is an append-only failure history backed by a store bucket.
type Log struct{ bucket bucket }

// New wraps a bucket as a failure Log.
func New(b bucket) *Log { return &Log{bucket: b} }

// Append records one failure. The store key is time-ordered (zero-padded
// nanoseconds) so repeated failures of the same URL are all retained and the
// keys sort chronologically.
func (l *Log) Append(r Record) error {
	if r.FailedAt.IsZero() {
		r.FailedAt = time.Now()
	}
	key := fmt.Sprintf("%020d|%s", r.FailedAt.UnixNano(), r.URL)
	return l.bucket.Put(key, r)
}

// AppendAll records a batch of failures, returning the first error (if any)
// while still attempting the rest.
func (l *Log) AppendAll(recs []Record) error {
	var firstErr error
	for _, r := range recs {
		if err := l.Append(r); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Query returns failures whose title or reason contains q (case-insensitive),
// newest first. A blank query returns the most recent failures regardless of
// title — failures are rare, so "show me what recently broke" is useful.
// limit <= 0 means no cap.
func (l *Log) Query(q string, limit int) ([]Record, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	recs, err := l.all()
	if err != nil {
		return nil, err
	}
	out := recs[:0]
	for _, r := range recs {
		if needle == "" ||
			strings.Contains(strings.ToLower(r.Title), needle) ||
			strings.Contains(strings.ToLower(r.Reason), needle) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FailedAt.After(out[j].FailedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (l *Log) all() ([]Record, error) {
	raw, err := l.bucket.All()
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(raw))
	for _, v := range raw {
		var r Record
		if err := json.Unmarshal(v, &r); err != nil {
			continue // skip malformed rows
		}
		out = append(out, r)
	}
	return out, nil
}

// RecordsFromRun is the run-finalizer convenience: it builds failure records
// for every failed entry in the result's entries, resolving each failing node
// from the run's traces. entries is the complete (uncapped) entry list; traces
// is the (capped) per-entry journey used only to name the failing node.
func RecordsFromRun(entries []*entry.Entry, traces []executor.EntryTrace, task string, at time.Time) []Record {
	nodeMap := failingNodeMap(traces)
	return RecordsFromEntries(entries, task, at, func(url string) string { return nodeMap[url] })
}

// failingNodeMap maps entry URL → the node that failed it, read from the last
// "failed" step of each trace.
func failingNodeMap(traces []executor.EntryTrace) map[string]string {
	m := make(map[string]string)
	for _, t := range traces {
		if t.Final != "failed" || t.URL == "" {
			continue
		}
		for i := len(t.Steps) - 1; i >= 0; i-- {
			if t.Steps[i].State == "failed" {
				m[t.URL] = t.Steps[i].Node
				break
			}
		}
	}
	return m
}

// RecordsFromEntries builds failure records for every failed entry in entries,
// stamping them with task and at. nodeFor, when non-nil, resolves the failing
// node for an entry URL (from the run trace); pass nil to leave Node empty.
func RecordsFromEntries(entries []*entry.Entry, task string, at time.Time, nodeFor func(url string) string) []Record {
	var out []Record
	for _, e := range entries {
		if !e.IsFailed() {
			continue
		}
		node := ""
		if nodeFor != nil {
			node = nodeFor(e.URL)
		}
		out = append(out, Record{
			Title:     e.Title,
			URL:       e.URL,
			Reason:    e.FailReason,
			Node:      node,
			Task:      task,
			MediaType: e.GetString(entry.FieldMediaType),
			FailedAt:  at,
		})
	}
	return out
}
