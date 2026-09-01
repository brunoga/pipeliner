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
	// Scope correlation to the pipelines that actually use each list, so a TV
	// favorite never picks up rejections from a movies pipeline (and vice versa).
	taskKinds, err := watchdog.LoadTaskKinds(s.db.Bucket(watchdog.TaskKindsBucket))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stuck := watchdog.Detect(occ, favorites, taskKinds, minRuns, watchdog.DefaultMaxDistance)
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
// filters (the same lists they match against), tagging each with its media kind
// so correlation stays scoped to the pipelines that use it.
func (s *Server) resolvedFavorites() []watchdog.Favorite {
	var out []watchdog.Favorite
	for _, b := range []struct {
		bucket string
		kind   watchdog.MediaKind
	}{
		{"cache_series_list", watchdog.KindSeries},
		{"cache_movies_list", watchdog.KindMovies},
	} {
		if lists, ok := cache.Values[[]match.TitleEntry](s.db.Bucket(b.bucket)); ok {
			for _, l := range lists {
				out = append(out, watchdog.MakeFavorites(l, b.kind)...)
			}
		}
	}
	return out
}
