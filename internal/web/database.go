package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
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

// apiDBGetBucket returns paginated entries in a bucket.
//
// Query parameters:
//
//	after=<key>  cursor — return entries whose key is strictly greater (first page if absent)
//	limit=<N>    page size (default 20, max 500)
//	q=<text>     case-insensitive substring filter applied to the key and JSON value
//
// Response fields: entries, next_cursor, has_more, total.
// For the series bucket, entries are grouped into shows (grouped field) and the cursor
// is a show name rather than a raw episode key.
func (s *Server) apiDBGetBucket(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	after := r.URL.Query().Get("after")
	q := r.URL.Query().Get("q")
	limit := 20
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}

	if name == "series" {
		s.apiDBGetSeriesBucket(w, r, after, q, limit)
		return
	}

	// ── key-level cursor pagination for all non-series buckets ─────────────────
	likeQ := "%" + strings.ToLower(q) + "%"

	// Total count (with optional filter).
	var total int
	if q != "" {
		s.db.DB().QueryRowContext(r.Context(), //nolint:errcheck
			`SELECT COUNT(*) FROM store WHERE bucket = ? AND (lower(key) LIKE ? OR lower(value) LIKE ?)`,
			name, likeQ, likeQ).Scan(&total)
	} else {
		s.db.DB().QueryRowContext(r.Context(), //nolint:errcheck
			`SELECT COUNT(*) FROM store WHERE bucket = ?`, name).Scan(&total)
	}

	// Fetch limit+1 rows so we can detect whether there is a next page.
	var (
		sqlStr string
		args   []any
	)
	switch {
	case q != "" && after != "":
		sqlStr = `SELECT key, value FROM store WHERE bucket = ? AND key > ? AND (lower(key) LIKE ? OR lower(value) LIKE ?) ORDER BY key LIMIT ?`
		args = []any{name, after, likeQ, likeQ, limit + 1}
	case q != "":
		sqlStr = `SELECT key, value FROM store WHERE bucket = ? AND (lower(key) LIKE ? OR lower(value) LIKE ?) ORDER BY key LIMIT ?`
		args = []any{name, likeQ, likeQ, limit + 1}
	case after != "":
		sqlStr = `SELECT key, value FROM store WHERE bucket = ? AND key > ? ORDER BY key LIMIT ?`
		args = []any{name, after, limit + 1}
	default:
		sqlStr = `SELECT key, value FROM store WHERE bucket = ? ORDER BY key LIMIT ?`
		args = []any{name, limit + 1}
	}

	rows, err := s.db.DB().QueryContext(r.Context(), sqlStr, args...)
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

	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	var nextCursor string
	if hasMore && len(entries) > 0 {
		nextCursor = entries[len(entries)-1].Key
	}
	if entries == nil {
		entries = []dbEntry{}
	}
	writeJSON(w, map[string]any{
		"entries":     entries,
		"next_cursor": nextCursor,
		"has_more":    hasMore,
		"total":       total,
	})
}

// apiDBGetSeriesBucket handles cursor pagination at the show level for the
// series tracker.  The cursor (after) is a show name, not a raw episode key.
func (s *Server) apiDBGetSeriesBucket(w http.ResponseWriter, r *http.Request, after, q string, limit int) {
	ctx := r.Context()
	likeQ := "%" + strings.ToLower(q) + "%"

	// Total distinct shows (with optional filter).
	var total int
	if q != "" {
		s.db.DB().QueryRowContext(ctx, //nolint:errcheck
			`SELECT COUNT(DISTINCT substr(key,1,instr(key,'|')-1))
			 FROM store WHERE bucket='series'
			 AND lower(substr(key,1,instr(key,'|')-1)) LIKE ?`, likeQ).Scan(&total)
	} else {
		s.db.DB().QueryRowContext(ctx, //nolint:errcheck
			`SELECT COUNT(DISTINCT substr(key,1,instr(key,'|')-1)) FROM store WHERE bucket='series'`).Scan(&total)
	}

	// Fetch limit+1 distinct show names after the cursor.
	var showSQL string
	var showArgs []any
	switch {
	case q != "" && after != "":
		showSQL = `SELECT DISTINCT substr(key,1,instr(key,'|')-1) FROM store
		           WHERE bucket='series' AND substr(key,1,instr(key,'|')-1) > ?
		           AND lower(substr(key,1,instr(key,'|')-1)) LIKE ?
		           ORDER BY 1 LIMIT ?`
		showArgs = []any{after, likeQ, limit + 1}
	case q != "":
		showSQL = `SELECT DISTINCT substr(key,1,instr(key,'|')-1) FROM store
		           WHERE bucket='series' AND lower(substr(key,1,instr(key,'|')-1)) LIKE ?
		           ORDER BY 1 LIMIT ?`
		showArgs = []any{likeQ, limit + 1}
	case after != "":
		showSQL = `SELECT DISTINCT substr(key,1,instr(key,'|')-1) FROM store
		           WHERE bucket='series' AND substr(key,1,instr(key,'|')-1) > ?
		           ORDER BY 1 LIMIT ?`
		showArgs = []any{after, limit + 1}
	default:
		showSQL = `SELECT DISTINCT substr(key,1,instr(key,'|')-1) FROM store
		           WHERE bucket='series' ORDER BY 1 LIMIT ?`
		showArgs = []any{limit + 1}
	}

	srows, err := s.db.DB().QueryContext(ctx, showSQL, showArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var shows []string
	for srows.Next() {
		var name string
		if srows.Scan(&name) == nil { //nolint:errcheck
			shows = append(shows, name)
		}
	}
	srows.Close()

	hasMore := len(shows) > limit
	if hasMore {
		shows = shows[:limit]
	}
	var nextCursor string
	if hasMore && len(shows) > 0 {
		nextCursor = shows[len(shows)-1]
	}

	if len(shows) == 0 {
		writeJSON(w, map[string]any{
			"entries":     []dbEntry{},
			"grouped":     []seriesShow{},
			"next_cursor": "",
			"has_more":    false,
			"total":       total,
		})
		return
	}

	// Fetch all episodes for the shows on this page.
	placeholders := strings.Repeat("?,", len(shows))
	placeholders = placeholders[:len(placeholders)-1]
	epSQL := `SELECT key, value FROM store WHERE bucket='series' AND substr(key,1,instr(key,'|')-1) IN (` + placeholders + `) ORDER BY key`
	epArgs := make([]any, len(shows))
	for i, s := range shows {
		epArgs[i] = s
	}
	erows, err := s.db.DB().QueryContext(ctx, epSQL, epArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer erows.Close()

	var entries []dbEntry
	for erows.Next() {
		var key, val string
		if err := erows.Scan(&key, &val); err != nil {
			continue
		}
		entries = append(entries, dbEntry{Key: key, Value: json.RawMessage(val)})
	}
	if entries == nil {
		entries = []dbEntry{}
	}
	writeJSON(w, map[string]any{
		"entries":     entries,
		"grouped":     groupSeries(entries),
		"next_cursor": nextCursor,
		"has_more":    hasMore,
		"total":       total,
	})
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
	displayNames := map[string]string{} // seriesName → DisplayName
	for _, e := range entries {
		var rec struct {
			SeriesName   string `json:"series_name"`
			DisplayName  string `json:"display_name"`
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
		if rec.DisplayName != "" {
			displayNames[rec.SeriesName] = rec.DisplayName
		}
	}
	shows := make([]seriesShow, 0, len(groups))
	for name, eps := range groups {
		sort.Slice(eps, func(i, j int) bool { return eps[i].EpisodeID < eps[j].EpisodeID })
		display := displayNames[name]
		if display == "" {
			display = name
		}
		shows = append(shows, seriesShow{Name: display, Episodes: eps})
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
