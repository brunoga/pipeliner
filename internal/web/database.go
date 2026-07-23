package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// bucketCategory classifies a bucket by its naming convention.
type bucketCategory string

const (
	catTracker  bucketCategory = "tracker"
	catCache    bucketCategory = "cache"
	catDiscover bucketCategory = "discover"
	catOther    bucketCategory = "other"
)

// trackerDisplayNames covers the well-known top-level trackers. Plugin caches
// no longer live here — they declare themselves via Descriptor.Caches and are
// merged in by the database tab handler. See cacheRegistry().
var trackerDisplayNames = map[string]string{
	"series":   "Series Tracker",
	"movies":   "Movie Tracker",
	"premiere": "Premiere Tracker",
}

// legacyCacheDisplayNames maps cache buckets that no current plugin declares
// (written by older pipeliner versions and possibly still present in a user's
// database) to a friendly label. Without this, the sidebar shows the raw
// bucket name (e.g. "cache_tvdb") among otherwise friendly cache names.
// These buckets are never merged into the sidebar when absent — the mapping
// only applies when the store actually contains them.
var legacyCacheDisplayNames = map[string]string{
	"cache_tvdb":        "TVDB Lookup Cache",
	"cache_tmdb":        "TMDb Lookup Cache",
	"cache_trakt":       "Trakt Lookup Cache",
	"cache_filter_tvdb": "TVDB Filter Cache",
	"cache_series_from": "Series From-List Cache",
	"cache_movies_from": "Movies From-List Cache",
}

// cacheRegistry walks every registered plugin descriptor and collects the
// (bucket → display name) pairs they declare. Duplicate names across plugins
// (e.g. cache_bluray_index is shared by metainfo_bluray and bluray_releases)
// are deduplicated — the first descriptor wins, which is deterministic
// because plugin.All() returns a name-sorted slice.
func cacheRegistry() map[string]string {
	out := map[string]string{}
	for _, d := range plugin.All() {
		for _, c := range d.Caches {
			if _, ok := out[c.Name]; ok {
				continue
			}
			out[c.Name] = c.Display
		}
	}
	return out
}

func classifyBucket(name string, registry map[string]string) bucketCategory {
	switch name {
	case "series", "movies":
		return catTracker
	}
	if strings.HasPrefix(name, "premiere:") {
		return catTracker
	}
	if _, ok := registry[name]; ok {
		return catCache
	}
	if strings.HasPrefix(name, "cache_") {
		return catCache
	}
	if strings.HasPrefix(name, "discover:") {
		return catDiscover
	}
	return catOther
}

func bucketDisplay(name string, registry map[string]string) string {
	if d, ok := trackerDisplayNames[name]; ok {
		return d
	}
	if d, ok := registry[name]; ok {
		return d
	}
	if d, ok := legacyCacheDisplayNames[name]; ok {
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
//
// The list is the union of:
//  1. Every bucket the SQLite store actually contains (with its real count).
//  2. Every cache bucket declared by a registered plugin via Descriptor.Caches
//     (added at count=0 if the plugin has not yet written to it).
//
// (2) is what lets the database tab show e.g. cache_bluray_index on a fresh
// install — before this, a bucket only appeared after its first Put, so users
// could not pre-emptively clear a cache they expected to exist.
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

	registry := cacheRegistry()
	seen := make(map[string]bool)
	var buckets []bucketInfo
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			continue
		}
		seen[name] = true
		buckets = append(buckets, bucketInfo{
			Name:     name,
			Display:  bucketDisplay(name, registry),
			Count:    count,
			Category: classifyBucket(name, registry),
		})
	}
	// Surface plugin-declared cache buckets that haven't been written yet.
	for name, display := range registry {
		if seen[name] {
			continue
		}
		buckets = append(buckets, bucketInfo{
			Name:     name,
			Display:  display,
			Count:    0,
			Category: catCache,
		})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
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

	// Total count (with optional filter). Scan error is intentionally ignored —
	// total defaults to 0 which is safe (pager shows 0 total, navigation disabled).
	var total int
	if q != "" {
		_ = s.db.DB().QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM store WHERE bucket = ? AND (lower(key) LIKE ? OR lower(value) LIKE ?)`,
			name, likeQ, likeQ).Scan(&total)
	} else {
		_ = s.db.DB().QueryRowContext(r.Context(),
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

	// Total distinct shows (with optional filter). Scan error intentionally ignored.
	var total int
	if q != "" {
		_ = s.db.DB().QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT substr(key,1,instr(key,'|')-1))
			 FROM store WHERE bucket='series'
			 AND lower(substr(key,1,instr(key,'|')-1)) LIKE ?`, likeQ).Scan(&total)
	} else {
		_ = s.db.DB().QueryRowContext(ctx,
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
	// Key is the exact stored bucket key (normalized series name + "|" +
	// episode ID). The UI must use this — not a reconstruction from the
	// display name — when deleting the row, because display and stored
	// names may differ (e.g. in case after the lowercasing migration).
	Key          string `json:"key"`
	EpisodeID    string `json:"episode_id"`
	Quality      string `json:"quality"`
	DownloadedAt string `json:"downloaded_at"`
}

type seriesShow struct {
	// Name is the human-friendly display name.
	Name string `json:"name"`
	// SeriesName is the normalized name used as key material in the store
	// (the part of the key before the '|'). Deletion APIs take this form.
	SeriesName string          `json:"series_name"`
	Episodes   []seriesEpisode `json:"episodes"`
}

// unrecognizedShowLabel is the display label for entries whose stored value
// could not be parsed as a series record and whose key carries no show name.
// The group still surfaces so the data remains visible and deletable.
const unrecognizedShowLabel = "(unrecognized entries)"

// seriesKeyPrefix returns the show portion of a stored series key
// ("show|S01E01" → "show"). Empty when the key has no separator.
func seriesKeyPrefix(key string) string {
	if i := strings.Index(key, "|"); i >= 0 {
		return key[:i]
	}
	return ""
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
		// Parse failures are tolerated: the entry is still grouped by its
		// key prefix (or under the "(unrecognized entries)" group) so it
		// can be inspected and deleted instead of silently disappearing.
		_ = json.Unmarshal(e.Value, &rec)

		// Group by the stored key prefix — that's what the pagination SQL
		// groups by and what prefix deletion matches. Fall back to the
		// record's own series_name for keys without a separator.
		name := seriesKeyPrefix(e.Key)
		if name == "" {
			name = rec.SeriesName
		}
		epID := rec.EpisodeID
		if epID == "" {
			if i := strings.Index(e.Key, "|"); i >= 0 {
				epID = e.Key[i+1:]
			} else {
				epID = e.Key
			}
		}
		groups[name] = append(groups[name], seriesEpisode{
			Key:          e.Key,
			EpisodeID:    epID,
			Quality:      rec.Quality.Str,
			DownloadedAt: rec.DownloadedAt,
		})
		if rec.DisplayName != "" {
			displayNames[name] = rec.DisplayName
		}
	}
	shows := make([]seriesShow, 0, len(groups))
	for name, eps := range groups {
		sort.Slice(eps, func(i, j int) bool { return eps[i].EpisodeID < eps[j].EpisodeID })
		display := displayNames[name]
		if display == "" {
			display = name
		}
		if display == "" {
			display = unrecognizedShowLabel
		}
		shows = append(shows, seriesShow{Name: display, SeriesName: name, Episodes: eps})
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

// apiDBDeleteSeriesShow deletes every episode of one show from the series
// bucket, matching on the normalized series name (the key prefix before '|').
// Deleting server-side avoids the client having to page through the whole
// bucket to collect keys — with >20 shows the UI only ever holds one page.
//
// The name is passed as a JSON body field (same rationale as
// apiDBDeleteEntry: show names may contain slashes or other characters that
// are awkward in a URL path). An empty series_name is valid and targets the
// "(unrecognized entries)" group: rows whose key has no '|' separator.
func (s *Server) apiDBDeleteSeriesShow(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	var req struct {
		SeriesName *string `json:"series_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SeriesName == nil {
		http.Error(w, "missing series_name", http.StatusBadRequest)
		return
	}
	// Exact match on the key prefix: substr up to the '|' separator, which is
	// how the pagination and grouping queries identify a show. For keys with
	// no separator, instr()=0 makes substr() return '' — matching the empty
	// series_name used by the unrecognized-entries group.
	if _, err := s.db.DB().ExecContext(r.Context(),
		`DELETE FROM store WHERE bucket = 'series'
		 AND substr(key, 1, CASE WHEN instr(key,'|') > 0 THEN instr(key,'|')-1 ELSE 0 END) = ?`,
		*req.SeriesName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
