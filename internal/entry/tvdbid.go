package entry

import (
	"strconv"
	"strings"
)

// TVDBID returns the entry's TheTVDB series id as a string, or "" when the
// entry carries none.
//
// The field is a string when a TheTVDB-backed plugin set it (tvdb_favorites,
// series_gaps, metainfo_tvdb, library_shows) and a JSON number when it came
// from Trakt, so both shapes are read here instead of at every call site.
// A non-positive id reads as absent: it identifies nothing, and comparing it
// would make two shows of unknown identity look like the same show.
func TVDBID(e *Entry) string {
	v, ok := e.Get("tvdb_id")
	if !ok {
		return ""
	}
	var s string
	switch id := v.(type) {
	case string:
		s = strings.TrimSpace(id)
	case int:
		s = strconv.Itoa(id)
	case int64:
		s = strconv.FormatInt(id, 10)
	case float64:
		s = strconv.Itoa(int(id))
	default:
		return ""
	}
	// Canonicalize the numeric form so an id that arrived as a number and the
	// same id that arrived as a string compare equal.
	if n, err := strconv.Atoi(s); err == nil {
		if n <= 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	return s
}
