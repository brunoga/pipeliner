package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/brunoga/pipeliner/internal/pipelinestate"
)

// liveTaskNames returns the names of the pipelines currently loaded. Returns
// nil when there are none, which pipelinestate reads as "do not judge" --
// before the first config is applied an empty list would call every bucket an
// orphan.
func (s *Server) liveTaskNames() []string {
	s.tasksMu.RLock()
	defer s.tasksMu.RUnlock()
	if len(s.tasks) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.tasks))
	for _, t := range s.tasks {
		names = append(names, t.Name)
	}
	return names
}

// orphanStateWarnings reports per-pipeline state whose pipeline no longer
// exists, as warnings appended to a config save.
//
// This is where a rename is caught. The store cannot tell a rename from a
// delete-plus-create, so the save that renames a pipeline is the moment the
// old name's seen records and trackers stop being reachable -- and the moment
// the person who did it is still looking. Saying nothing here is how a rename
// comes to mean "re-download everything".
func (s *Server) orphanStateWarnings() []string {
	if s.db == nil {
		return nil
	}
	orphans, err := pipelinestate.Orphans(s.db, s.liveTaskNames())
	if err != nil || len(orphans) == 0 {
		return nil
	}
	out := make([]string, 0, len(orphans))
	for _, o := range orphans {
		out = append(out, fmt.Sprintf(
			"pipeline state for %q is orphaned: %d records in %s, and no pipeline has that name. "+
				"If it was renamed, adopt the state into the new name; if it is gone for good, forget it. "+
				"Until then it is kept, not deleted.",
			o.Pipeline, o.Rows(), bucketSummary(o)))
	}
	return out
}

func bucketSummary(st pipelinestate.State) string {
	s := ""
	for i, b := range st.Buckets {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s (%d)", b.Bucket, b.Rows)
	}
	return s
}

// apiPipelineState lists per-pipeline state, flagging what no config claims.
func (s *Server) apiPipelineState(w http.ResponseWriter, _ *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	states, err := pipelinestate.Scan(s.db, s.liveTaskNames())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type resp struct {
		Pipeline string `json:"pipeline"`
		Orphaned bool   `json:"orphaned"`
		Rows     int    `json:"rows"`
		Buckets  []struct {
			Bucket string `json:"bucket"`
			Label  string `json:"label"`
			Rows   int    `json:"rows"`
		} `json:"buckets"`
	}
	out := make([]resp, 0, len(states))
	for _, st := range states {
		r := resp{Pipeline: st.Pipeline, Orphaned: st.Orphaned, Rows: st.Rows()}
		for _, b := range st.Buckets {
			r.Buckets = append(r.Buckets, struct {
				Bucket string `json:"bucket"`
				Label  string `json:"label"`
				Rows   int    `json:"rows"`
			}{b.Bucket, b.Label, b.Rows})
		}
		out = append(out, r)
	}
	writeJSON(w, map[string]any{"pipelines": out})
}

// apiPipelineStateMove moves one pipeline's state onto another name: a rename
// when the destination has none, an adoption when it does.
//
// Exposed over the API because the CLI cannot be the only way in: the store
// takes an exclusive lock at open time, so a command-line move means stopping
// the daemon, and a rename is usually made in the visual editor with the
// daemon running.
func (s *Server) apiPipelineStateMove(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	var req struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Prefer string `json:"prefer"`
		DryRun bool   `json:"dry_run"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	rep, err := pipelinestate.Move(s.db, req.From, req.To, pipelinestate.MoveOptions{
		Prefer: pipelinestate.Side(req.Prefer),
		DryRun: req.DryRun,
	})
	if err != nil {
		// A collision that needs a choice is the caller's to resolve, not a
		// server fault.
		code := http.StatusBadRequest
		if errors.Is(err, pipelinestate.ErrNeedPrefer) {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, rep)
}

// apiPipelineStateForget deletes a pipeline's state. There is no undo short of
// a database backup, so the callers confirm before asking.
func (s *Server) apiPipelineStateForget(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	// Refuse to drop the state of a pipeline that still exists: that is not a
	// lifecycle operation, it is clearing a tracker, which the database tab
	// already does one bucket at a time and with its own confirmation.
	for _, live := range s.liveTaskNames() {
		if live == name {
			http.Error(w, fmt.Sprintf("pipeline %q still exists; forget is for state whose pipeline is gone", name),
				http.StatusConflict)
			return
		}
	}
	rep, err := pipelinestate.Forget(s.db, name, r.URL.Query().Get("dry_run") == "true")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, rep)
}
