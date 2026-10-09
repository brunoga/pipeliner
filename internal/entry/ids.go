package entry

import (
	"strconv"
	"strings"
)

// Provider id fields, in the order they are trusted.
//
// The first group is an id pipeliner resolved itself (series_gaps asking
// TheTVDB for a specific series, metainfo_tvdb/metainfo_tmdb resolving one) or
// took from a list provider's own metadata for the item. The last is the id an
// indexer published alongside a release, which is a release-level claim about
// what the file contains: better than nothing and better than a name search,
// but it loses to an id we established ourselves.
var (
	tvdbIDFields = []string{"tvdb_id", "trakt_tvdb_id", "jackett_tvdb_id"}
	tmdbIDFields = []string{"tmdb_id", "trakt_tmdb_id", "jackett_tmdb_id"}
	imdbIDFields = []string{FieldVideoImdbID, "trakt_imdb_id", "jackett_imdb_id"}

	// Trakt's own id has one home: nothing but Trakt issues it.
	traktIDFields = []string{"trakt_id"}
)

// TVDBID returns the entry's TheTVDB series id as a string, or "" when the
// entry carries none. See [providerID] for the shapes read and [tvdbIDFields]
// for the precedence.
func TVDBID(e *Entry) string { return providerID(e, tvdbIDFields, numericID) }

// TMDBID returns the entry's TMDB id as a string, or "" when the entry
// carries none.
func TMDBID(e *Entry) string { return providerID(e, tmdbIDFields, numericID) }

// TraktID returns the entry's Trakt id as a string, or "" when the entry
// carries none.
func TraktID(e *Entry) string { return providerID(e, traktIDFields, numericID) }

// IMDBID returns the entry's IMDb id (e.g. "tt1375666"), or "" when the entry
// carries none. IMDb ids are opaque strings rather than numbers, so they are
// compared case-insensitively and otherwise passed through as published.
func IMDBID(e *Entry) string { return providerID(e, imdbIDFields, imdbID) }

// providerID returns the first id the entry publishes among fields, in order,
// normalized by norm. A field whose value normalizes to "" is skipped rather
// than ending the search: a plugin that declares an id field and sets it empty
// must not hide an id a later field does carry.
func providerID(e *Entry, fields []string, norm func(string) string) string {
	if e == nil {
		return ""
	}
	for _, f := range fields {
		v, ok := e.Get(f)
		if !ok {
			continue
		}
		if id := norm(rawID(v)); id != "" {
			return id
		}
	}
	return ""
}

// rawID renders a field value as a string. The same id arrives as a string
// from the TheTVDB- and TMDB-backed plugins, as a JSON number from Trakt and
// from anything pushed at the ingest API, and as a float64 after a round trip
// through the store — so all of those are read here instead of at every call
// site, where one of them is always the shape that gets forgotten.
func rawID(v any) string {
	switch id := v.(type) {
	case string:
		return strings.TrimSpace(id)
	case int:
		return strconv.Itoa(id)
	case int64:
		return strconv.FormatInt(id, 10)
	case float64:
		if id != float64(int64(id)) {
			return "" // not an id; a fractional value is something else
		}
		return strconv.FormatInt(int64(id), 10)
	}
	return ""
}

// numericID canonicalizes a numeric provider id so the same id compares equal
// whichever shape it arrived in. A non-positive id reads as absent: it
// identifies nothing, and comparing it would make two items of unknown
// identity look like the same item.
func numericID(s string) string {
	if s == "" {
		return ""
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		// Not a number after all. Keep it rather than discard it — two equal
		// strings still name the same item — but reject an obvious non-id.
		return s
	}
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// imdbID normalizes an IMDb id. "tt0000000" is IMDb's own placeholder for
// "unknown" and reads as absent.
func imdbID(s string) string {
	s = strings.ToLower(s)
	if s == "" || strings.Trim(s, "t0") == "" {
		return ""
	}
	return s
}
