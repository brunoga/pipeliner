package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
)

// apiDBMarkSeries seeds the series tracker with an episode as if it had already
// been downloaded, so the series filter treats it as seen and never grabs it.
// This is the inverse of deleting a tracker row (which forces a re-download):
// it lets a user tell pipeliner "I already have this" for episodes acquired
// outside the tool.
//
// Request body (POST /api/db/series/mark):
//
//	{ "show": "Star Trek: Strange New Worlds", "episode_id": "S04E05",
//	  "quality": "1080p web h264" }
//
// The show name is normalized and the episode ID canonicalized exactly as the
// filter would, so the stored key matches future lookups. quality is optional.
func (s *Server) apiDBMarkSeries(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	var req struct {
		Show      string `json:"show"`
		EpisodeID string `json:"episode_id"`
		Quality   string `json:"quality"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	norm := match.Normalize(req.Show)
	if norm == "" {
		http.Error(w, "show is required", http.StatusBadRequest)
		return
	}
	epID, ok := series.CanonicalEpisodeID(req.EpisodeID)
	if !ok {
		http.Error(w, "episode_id must be a season/episode (S04E05), absolute (EP012), or date (2023-11-15)", http.StatusBadRequest)
		return
	}
	tracker := series.NewTracker(s.db.Bucket(series.TrackerBucketName))
	_, existed := tracker.Get(norm, epID)
	rec := series.Record{
		SeriesName:   norm,
		DisplayName:  req.Show,
		EpisodeID:    epID,
		DownloadedAt: time.Now(),
		Quality:      quality.Parse(req.Quality),
	}
	if err := tracker.Mark(rec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"key":     norm + "|" + epID,
		"existed": existed,
		"record":  rec,
	})
}

// apiDBMarkMovie seeds the movies tracker with a movie as already downloaded.
// Request body (POST /api/db/movies/mark):
//
//	{ "title": "Furiosa", "year": 2024, "is_3d": false, "quality": "1080p bluray" }
func (s *Server) apiDBMarkMovie(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	var req struct {
		Title   string `json:"title"`
		Year    int    `json:"year"`
		Is3D    bool   `json:"is_3d"`
		Quality string `json:"quality"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	norm := match.Normalize(req.Title)
	if norm == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	tracker := movies.NewTracker(s.db.Bucket(movies.TrackerBucketName))
	existed := tracker.IsSeen(norm, req.Year, req.Is3D)
	rec := movies.Record{
		Title:        norm,
		Year:         req.Year,
		Is3D:         req.Is3D,
		DownloadedAt: time.Now(),
		Quality:      quality.Parse(req.Quality),
	}
	if err := tracker.Mark(rec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"existed": existed, "record": rec})
}
