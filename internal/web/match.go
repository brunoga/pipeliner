package web

import (
	"encoding/json"
	"net/http"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/match"
)

// apiMatchTest diagnoses why a title does or does not match a list, mirroring
// the `pipeliner match` CLI. It is the fast answer to "why isn't show X
// downloading?" — normalization mismatches (a colon, an ampersand) show up
// immediately instead of after a log dive.
//
// Request body (POST /api/match/test):
//
//	{
//	  "input":      "Star Trek Strange New Worlds",  // required
//	  "year":       0,                                // optional, for movies
//	  "bucket":     "cache_series_list",              // optional: resolve
//	                                                  //   candidates from this
//	                                                  //   store cache bucket
//	  "candidates": ["Silo", "Star Trek: Strange New Worlds"]  // optional
//	}
//
// Exactly one candidate source is used: if "bucket" is non-empty the resolved
// title list cached in that bucket is used (the same list the filter matches
// against); otherwise the explicit "candidates" strings are used.
//
// Response is a match.ProbeResult.
func (s *Server) apiMatchTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input      string   `json:"input"`
		Year       int      `json:"year"`
		Bucket     string   `json:"bucket"`
		Candidates []string `json:"candidates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Input == "" {
		http.Error(w, "input is required", http.StatusBadRequest)
		return
	}

	var candidates []match.TitleEntry
	if req.Bucket != "" {
		if s.db == nil {
			http.Error(w, "database not available", http.StatusNotImplemented)
			return
		}
		lists, ok := cache.Values[[]match.TitleEntry](s.db.Bucket(req.Bucket))
		if !ok {
			http.Error(w, "bucket does not support bulk read", http.StatusBadRequest)
			return
		}
		for _, l := range lists {
			candidates = append(candidates, l...)
		}
	} else {
		for _, c := range req.Candidates {
			if c == "" {
				continue
			}
			candidates = append(candidates, match.NewTitleEntry(c, 0))
		}
	}

	writeJSON(w, match.Probe(req.Input, req.Year, candidates))
}
