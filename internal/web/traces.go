package web

import (
	"net/http"

	"github.com/brunoga/pipeliner/internal/traces"
)

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
