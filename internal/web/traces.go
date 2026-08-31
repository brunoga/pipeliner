package web

import (
	"net/http"
	"strconv"

	"github.com/brunoga/pipeliner/internal/traces"
)

// traceSearchDefaultLimit caps how many occurrences a title search returns by
// default, so a common word doesn't return every entry across all kept runs.
const traceSearchDefaultLimit = 200

// SetTraceStore wires the run-trace store into the inspector endpoints.
func (s *Server) SetTraceStore(ts *traces.Store) { s.traceStore = ts }

// apiTraceList handles GET /api/traces/{task}: metadata for the kept runs.
func (s *Server) apiTraceList(w http.ResponseWriter, r *http.Request) {
	if s.traceStore == nil {
		http.Error(w, "tracing not available", http.StatusNotImplemented)
		return
	}
	metas, err := s.traceStore.List(r.PathValue("task"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if metas == nil {
		metas = []traces.Meta{}
	}
	writeJSON(w, map[string]any{"runs": metas})
}

// apiTraceSearch handles GET /api/traces/search?q=<title>&limit=<n>: every
// occurrence of entries whose title matches q across all kept runs, newest
// first. Answers "was X ever grabbed, and what happened each time?" — history
// the single-record trackers cannot, since they keep only the latest download.
func (s *Server) apiTraceSearch(w http.ResponseWriter, r *http.Request) {
	if s.traceStore == nil {
		http.Error(w, "tracing not available", http.StatusNotImplemented)
		return
	}
	q := r.URL.Query().Get("q")
	limit := traceSearchDefaultLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	occ, err := s.traceStore.Search(q, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if occ == nil {
		occ = []traces.Occurrence{}
	}
	writeJSON(w, map[string]any{"occurrences": occ})
}

// apiTraceGet handles GET /api/traces/{task}/{run}: one run's full trace.
func (s *Server) apiTraceGet(w http.ResponseWriter, r *http.Request) {
	if s.traceStore == nil {
		http.Error(w, "tracing not available", http.StatusNotImplemented)
		return
	}
	rt, err := s.traceStore.Get(r.PathValue("task"), r.PathValue("run"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, rt)
}
