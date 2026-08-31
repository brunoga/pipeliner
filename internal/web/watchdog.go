package web

import (
	"net/http"
	"strconv"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/watchdog"
)

// apiWatchdogStuck reports favorites whose candidates keep appearing in runs but
// never get accepted — the silent-failure mode behind the SNW incident. It
// reads the resolved favorite lists from the title-list caches and correlates
// them with the kept run traces.
//
// GET /api/watchdog/stuck?min_runs=N
func (s *Server) apiWatchdogStuck(w http.ResponseWriter, r *http.Request) {
	if s.traceStore == nil {
		http.Error(w, "tracing not available", http.StatusNotImplemented)
		return
	}
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	minRuns := watchdog.DefaultMinRuns
	if v := r.URL.Query().Get("min_runs"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minRuns = n
		}
	}

	favorites := s.resolvedFavorites()
	occ, err := s.traceStore.AllOccurrences()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stuck := watchdog.Detect(occ, favorites, minRuns, watchdog.DefaultMaxDistance)
	if stuck == nil {
		stuck = []watchdog.StuckFavorite{}
	}
	writeJSON(w, map[string]any{
		"stuck":          stuck,
		"min_runs":       minRuns,
		"favorite_count": len(favorites),
	})
}

// resolvedFavorites unions the title lists cached by the series and movies
// filters (the same lists they match against).
func (s *Server) resolvedFavorites() []match.TitleEntry {
	var out []match.TitleEntry
	for _, bucket := range []string{"cache_series_list", "cache_movies_list"} {
		if lists, ok := cache.Values[[]match.TitleEntry](s.db.Bucket(bucket)); ok {
			for _, l := range lists {
				out = append(out, l...)
			}
		}
	}
	return out
}
