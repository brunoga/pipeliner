package web

import (
	"net/http"

	"github.com/brunoga/pipeliner/internal/downloads"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/series"
)

// apiDownloads answers "was this ever downloaded, and how many times?" from the
// download history log, grouped per item with each item flagged for whether it
// is still in its tracker. This surfaces re-downloads (quality upgrades) that
// the single-record trackers overwrite and cannot report.
//
// GET /api/downloads?q=<title>
func (s *Server) apiDownloads(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	q := r.URL.Query().Get("q")
	events, err := downloads.New(s.db.Bucket(downloads.BucketName)).Query(q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hist := downloads.GroupByItem(events)

	st := series.NewTracker(s.db.Bucket(series.TrackerBucketName))
	mt := movies.NewTracker(s.db.Bucket(movies.TrackerBucketName))

	type item struct {
		downloads.ItemHistory
		CurrentlyTracked bool `json:"currently_tracked"`
	}
	items := make([]item, 0, len(hist))
	for _, h := range hist {
		tracked := st.IsSeen(h.Name, h.EpisodeID)
		if h.MediaType == "movie" {
			tracked = mt.IsSeen(h.Name, h.Year, h.Is3D)
		}
		items = append(items, item{ItemHistory: h, CurrentlyTracked: tracked})
	}
	writeJSON(w, map[string]any{"items": items})
}
