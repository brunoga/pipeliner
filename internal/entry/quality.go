package entry

import "github.com/brunoga/pipeliner/internal/quality"

// FieldQuality is the Fields key under which the parsed quality.Quality struct
// is stored. It is populated by metainfo_file (and any other plugin that parses
// quality from the entry title) so downstream consumers — premiere, series,
// movies, quality filter, dedup — can use the structured form without
// re-parsing the title.
//
// The public string fields (video_quality, video_resolution, video_source,
// video_is_3d) remain the canonical form for conditions, templates, and the
// web UI; this field is the typed companion for code that needs the enums.
const FieldQuality = "_quality"

// SetQuality stores q on the entry under FieldQuality. Use Quality() to read
// it back with the type preserved.
func (e *Entry) SetQuality(q quality.Quality) {
	if e.Fields == nil {
		e.Fields = make(map[string]any)
	}
	e.Fields[FieldQuality] = q
}

// Quality returns the parsed quality.Quality previously stored by SetQuality.
// The second return value is false when no struct was stored or when the value
// at FieldQuality is of an unexpected type (e.g. after JSON deserialization
// through a string-typed map).
func (e *Entry) Quality() (quality.Quality, bool) {
	if e.Fields == nil {
		return quality.Quality{}, false
	}
	v, ok := e.Fields[FieldQuality]
	if !ok {
		return quality.Quality{}, false
	}
	q, ok := v.(quality.Quality)
	return q, ok
}
