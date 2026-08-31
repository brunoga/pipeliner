package web

import (
	"net/http"
	"strconv"

	"github.com/brunoga/pipeliner/internal/failures"
)

// failuresDefaultLimit caps how many failures the endpoint returns by default.
const failuresDefaultLimit = 200

// apiFailures returns the durable failure audit log — entries that failed at a
// sink — newest first. A blank q returns the most recent failures; a non-blank
// q filters by title or reason. This is the permanent counterpart to the
// bounded run-trace inspector.
//
// GET /api/failures?q=<text>&limit=<n>
func (s *Server) apiFailures(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	limit := failuresDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	recs, err := failures.New(s.db.Bucket(failures.BucketName)).Query(r.URL.Query().Get("q"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if recs == nil {
		recs = []failures.Record{}
	}
	writeJSON(w, map[string]any{"failures": recs})
}
