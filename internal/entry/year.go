package entry

// ReleaseYear extracts the canonical release year from an entry's fields.
// It reads FieldVideoYear, which is set by list sources (e.g. trakt_list) and
// metainfo enrichers (metainfo_tmdb, metainfo_tvdb). Returns 0 if absent.
func ReleaseYear(e *Entry) int {
	if v, ok := e.Get(FieldVideoYear); ok {
		switch y := v.(type) {
		case int:
			if y > 0 {
				return y
			}
		case int64:
			if y > 0 {
				return int(y)
			}
		case float64:
			if y > 0 {
				return int(y)
			}
		}
	}
	return 0
}
