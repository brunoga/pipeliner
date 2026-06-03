// Package dateparse provides best-effort parsing of human-readable date
// strings as they appear in RSS feeds, torrent metadata, and similar
// loosely-typed sources.
package dateparse

import "time"

// Layouts is the ordered list of formats tried by Parse. The order matters:
// more specific layouts are tried first so that "2006-01-02T15:04:05" doesn't
// claim a value that should parse as RFC3339.
var Layouts = []string{
	time.RFC3339,
	time.RFC822,
	time.RFC822Z,
	time.RFC1123,
	time.RFC1123Z,
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"02 Jan 2006",
	"January 2, 2006",
	"Jan 2, 2006",
}

// Parse tries each layout in order and returns the first successful parse.
// Returns (zero, false) when no layout matches.
func Parse(s string) (time.Time, bool) {
	for _, layout := range Layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// FromAny returns the time encoded in v. A time.Time is returned as-is; a
// string is run through Parse. Other types return (zero, false).
func FromAny(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		return Parse(t)
	}
	return time.Time{}, false
}
