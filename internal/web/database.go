package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// bucketCategory classifies a bucket by its naming convention.
type bucketCategory string

const (
	catTracker  bucketCategory = "tracker"
	catCache    bucketCategory = "cache"
	catDiscover bucketCategory = "discover"
	catOther    bucketCategory = "other"
)

var bucketDisplayNames = map[string]string{
	"series":                  "Series Tracker",
	"movies":                  "Movie Tracker",
	"premiere":                "Premiere Tracker",
	"upgrade":                 "Upgrade Tracker",
	"cache_metainfo_tvdb":     "TVDB Search Cache",
	"cache_metainfo_tvdb_ext": "TVDB Extended Cache",
	"cache_metainfo_tvdb_eps": "TVDB Episodes Cache",
	"cache_metainfo_tmdb":     "TMDb Cache",
	"cache_metainfo_trakt":    "Trakt Metainfo Cache",
	"cache_filter_tvdb":       "TVDB Filter Cache",
	"cache_series_from":       "Series From-Sources Cache",
	"cache_movies_from":       "Movies From-Sources Cache",
}

func classifyBucket(name string) bucketCategory {
	switch name {
	case "series", "movies", "upgrade":
		return catTracker
	}
	if strings.HasPrefix(name, "premiere:") {
		return catTracker
	}
	if strings.HasPrefix(name, "cache_") {
		return catCache
	}
	if strings.HasPrefix(name, "discover:") {
		return catDiscover
	}
	return catOther
}

func bucketDisplay(name string) string {
	if d, ok := bucketDisplayNames[name]; ok {
		return d
	}
	if rest, ok := strings.CutPrefix(name, "premiere:"); ok {
		return "Premiere Tracker: " + rest
	}
	if rest, ok := strings.CutPrefix(name, "discover:"); ok {
		return "Discover: " + rest
	}
	return name
}

// dbEntry is a raw key/value pair from the store.
type dbEntry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// apiDBBuckets returns all buckets with entry counts and category info.
func (s *Server) apiDBBuckets(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	rows, err := s.db.DB().QueryContext(r.Context(),
		`SELECT bucket, COUNT(*) FROM store GROUP BY bucket ORDER BY bucket`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type bucketInfo struct {
		Name     string         `json:"name"`
		Display  string         `json:"display"`
		Count    int            `json:"count"`
		Category bucketCategory `json:"category"`
	}
	var buckets []bucketInfo
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			continue
		}
		buckets = append(buckets, bucketInfo{
			Name:     name,
			Display:  bucketDisplay(name),
			Count:    count,
			Category: classifyBucket(name),
		})
	}
	if buckets == nil {
		buckets = []bucketInfo{}
	}
	writeJSON(w, map[string]any{"buckets": buckets})
}

// apiDBGetBucket returns all entries in a bucket, optionally with a
// show-grouped view for the series tracker.
func (s *Server) apiDBGetBucket(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	rows, err := s.db.DB().QueryContext(r.Context(),
		`SELECT key, value FROM store WHERE bucket = ? ORDER BY key`, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var entries []dbEntry
	for rows.Next() {
		var key, val string
		if err := rows.Scan(&key, &val); err != nil {
			continue
		}
		entries = append(entries, dbEntry{Key: key, Value: json.RawMessage(val)})
	}
	if entries == nil {
		entries = []dbEntry{}
	}

	if name == "series" {
		writeJSON(w, map[string]any{
			"entries": entries,
			"grouped": groupSeries(entries),
		})
		return
	}
	writeJSON(w, map[string]any{"entries": entries})
}

type seriesEpisode struct {
	EpisodeID    string `json:"episode_id"`
	Quality      string `json:"quality"`
	DownloadedAt string `json:"downloaded_at"`
}

type seriesShow struct {
	Name     string          `json:"name"`
	Episodes []seriesEpisode `json:"episodes"`
}

func groupSeries(entries []dbEntry) []seriesShow {
	groups := map[string][]seriesEpisode{}
	for _, e := range entries {
		var rec struct {
			SeriesName   string `json:"series_name"`
			EpisodeID    string `json:"episode_id"`
			DownloadedAt string `json:"downloaded_at"`
			Quality      struct {
				Str string `json:"string"`
			} `json:"quality"`
		}
		if err := json.Unmarshal(e.Value, &rec); err != nil {
			continue
		}
		groups[rec.SeriesName] = append(groups[rec.SeriesName], seriesEpisode{
			EpisodeID:    rec.EpisodeID,
			Quality:      rec.Quality.Str,
			DownloadedAt: rec.DownloadedAt,
		})
	}
	shows := make([]seriesShow, 0, len(groups))
	for name, eps := range groups {
		sort.Slice(eps, func(i, j int) bool { return eps[i].EpisodeID < eps[j].EpisodeID })
		shows = append(shows, seriesShow{Name: name, Episodes: eps})
	}
	sort.Slice(shows, func(i, j int) bool { return shows[i].Name < shows[j].Name })
	return shows
}

// apiDBClearBucket deletes all entries in a bucket.
func (s *Server) apiDBClearBucket(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	if _, err := s.db.DB().ExecContext(r.Context(),
		`DELETE FROM store WHERE bucket = ?`, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiDBDeleteEntry deletes a single key from a bucket.
// The key is passed as a JSON body field to avoid URL-encoding issues
// with keys that contain slashes, pipes, or other special characters.
func (s *Server) apiDBDeleteEntry(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	if _, err := s.db.DB().ExecContext(r.Context(),
		`DELETE FROM store WHERE bucket = ? AND key = ?`, name, req.Key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
