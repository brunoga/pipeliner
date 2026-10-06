package web

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/brunoga/pipeliner/internal/ingest"
)

// maxIngestBody caps a single push payload.
const maxIngestBody = 1 << 20 // 1 MiB

// SetIngestToken enables the push-ingest endpoint. When empty (the default)
// the endpoint answers 404, indistinguishable from not existing.
func (s *Server) SetIngestToken(token string) { s.ingestToken = token }

// apiIngest handles POST /api/ingest and POST /api/ingest/{queue}:
// bearer-token-authenticated machine pushes (autobrr, IRC bridges, …). The
// body is one item or an array of items: {"title": "...", "url": "...",
// "fields": {...}}. Items without a title are rejected. With
// ?pipeline=<name>, the named pipeline is triggered after the items are
// queued so the push takes effect immediately.
//
// The queue need not be given: it is a property of the pipeline's webhook
// source, not independent information, so naming the pipeline is enough
// whenever that pipeline has exactly one webhook source. resolveIngestQueue
// works out which, and refuses rather than guessing — see its comment for why
// a mismatch used to look like success.
//
// This route is mounted on the unauthenticated mux: browsers never call it,
// and machines can't do session login. The token is the whole auth story —
// treat it like a password.
func (s *Server) apiIngest(w http.ResponseWriter, r *http.Request) {
	if s.ingestToken == "" {
		http.NotFound(w, r)
		return
	}
	auth := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(tok), []byte(s.ingestToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	pipeline := r.URL.Query().Get("pipeline")
	queue, queueWarning, err := s.resolveIngestQueue(r.PathValue("queue"), pipeline)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxIngestBody)
	dec := json.NewDecoder(body)
	var items []ingest.Item
	// Accept a single object or an array.
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			http.Error(w, "invalid json array: "+err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		var one ingest.Item
		if err := json.Unmarshal(raw, &one); err != nil {
			http.Error(w, "invalid json object: "+err.Error(), http.StatusBadRequest)
			return
		}
		items = []ingest.Item{one}
	}

	valid := items[:0]
	rejected := 0
	for _, it := range items {
		if it.Title == "" {
			rejected++
			continue
		}
		valid = append(valid, it)
	}
	accepted, dropped := ingest.Enqueue(queue, valid)

	triggered := false
	if pipeline != "" && accepted > 0 && s.daemon != nil {
		s.daemon.Trigger(pipeline, false)
		triggered = true
	}

	out := map[string]any{
		"queue":     queue,
		"queued":    accepted,
		"dropped":   dropped,
		"rejected":  rejected,
		"triggered": triggered,
	}
	if queueWarning != "" {
		out["warning"] = queueWarning
	}
	writeJSON(w, out)
}

// resolveIngestQueue works out which queue a push belongs in, from the queue
// the caller named (possibly none) and the pipeline it named (possibly none).
//
// The queue is not independent information: it is the "queue" config of the
// pipeline's webhook source. So a caller who names the pipeline need not also
// name the queue, and requiring both only created a second thing to get
// wrong. Nothing used to check the two agreed, which made a typo in either
// look like success — items queued where nothing drained them while the
// response still reported them queued and the pipeline triggered, or items
// queued correctly and nothing ever run because the named pipeline was not
// the one with the webhook (and an on-demand pipeline has no schedule to
// catch it later).
//
// Where the pipeline has more than one webhook source the queue genuinely is
// ambiguous, and this refuses rather than picking one — the config-load
// warning in internal/dag says so before the first request ever gets here.
func (s *Server) resolveIngestQueue(queue, pipeline string) (string, string, error) {
	if queue == "" && pipeline == "" {
		return "", "", errors.New("name a queue (POST /api/ingest/{queue}) or a pipeline (?pipeline=...)")
	}

	s.tasksMu.RLock()
	tasks := make([]TaskInfo, len(s.tasks))
	copy(tasks, s.tasks)
	s.tasksMu.RUnlock()

	// No task list yet: a push can arrive in the window before the first
	// SetTasks call, and there is nothing to validate against. Take what the
	// caller gave rather than refusing a request that may well be correct —
	// but a queue cannot be derived from nothing, so that case is still an
	// error rather than a guess.
	if len(tasks) == 0 {
		if queue == "" {
			return "", "", fmt.Errorf("cannot derive a queue for pipeline %q: no pipelines are loaded yet", pipeline)
		}
		return queue, "", nil
	}

	// No pipeline named: the caller is queueing for a later scheduled run.
	// Accept it, but not into a queue nothing reads — that is a typo with no
	// legitimate reading, and silently black-holing the items is the failure
	// this function exists to remove.
	// No pipeline named: the caller is queueing for a later scheduled run, or
	// is an external pusher (autobrr, an IRC bridge) that only ever knew the
	// queue. Take it either way — refusing would break those pushers, and
	// would fail every push in the window before the first SetTasks call,
	// when the task list is simply not known yet. But say so when nothing
	// drains it, so the items are not black-holed silently.
	if pipeline == "" {
		var known []string
		for _, t := range tasks {
			for _, q := range t.Queues {
				if q == queue {
					return queue, "", nil
				}
				known = append(known, q)
			}
		}
		if len(known) == 0 {
			return queue, "", nil // task list unknown or no webhook anywhere: nothing to check against
		}
		return queue, fmt.Sprintf("no pipeline drains queue %q (queues: %s) — the items are queued but nothing will read them",
			queue, strings.Join(dedupeSorted(known), ", ")), nil
	}

	var task *TaskInfo
	for i := range tasks {
		if tasks[i].Name == pipeline {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		names := make([]string, 0, len(tasks))
		for _, t := range tasks {
			names = append(names, t.Name)
		}
		return "", "", fmt.Errorf("no such pipeline %q (pipelines: %s)", pipeline, strings.Join(dedupeSorted(names), ", "))
	}

	switch {
	case len(task.Queues) == 0:
		return "", "", fmt.Errorf("pipeline %q has no webhook source, so nothing can be pushed to it", pipeline)

	case queue == "" && len(task.Queues) == 1:
		return task.Queues[0], "", nil

	case queue == "":
		return "", "", fmt.Errorf("pipeline %q has %d webhook sources, so the queue must be named explicitly (queues: %s)",
			pipeline, len(task.Queues), strings.Join(dedupeSorted(task.Queues), ", "))
	}

	for _, q := range task.Queues {
		if q == queue {
			return queue, "", nil
		}
	}
	return "", "", fmt.Errorf("pipeline %q does not drain queue %q (its queues: %s)",
		pipeline, queue, strings.Join(dedupeSorted(task.Queues), ", "))
}

// dedupeSorted makes an error message's list stable and free of repeats, since
// two pipelines may drain the same queue.
func dedupeSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
