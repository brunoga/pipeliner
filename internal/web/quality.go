package web

import (
	"encoding/json"
	"net/http"

	"github.com/brunoga/pipeliner/internal/quality"
)

// apiQualityTest parses a release title into a quality and (optionally) reports,
// dimension by dimension, whether it satisfies a spec — the quality-side
// companion to apiMatchTest, mirroring the `pipeliner quality` CLI.
//
// Request body (POST /api/quality/test):
//
//	{ "title": "Show S01E01 720p WEB-DL x265", "spec": "1080p+" }
//
// An empty spec matches everything (all dimensions unconstrained), so the
// response then just reports the detected quality. Response is a
// quality.SpecResult.
func (s *Server) apiQualityTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
		Spec  string `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	spec, err := quality.ParseSpec(req.Spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, spec.Explain(quality.Parse(req.Title)))
}
