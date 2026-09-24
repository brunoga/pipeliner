package settle

import (
	"fmt"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
)

// MovieBucketName and SeriesBucketName hold each filter's settle windows.
const (
	MovieBucketName  = "movies_settle"
	SeriesBucketName = "series_settle"
)

// MovieKey builds the settle key for a movie, matching the download
// tracker's key shape so a title's window and its download record line up.
func MovieKey(title string, year int, is3D bool) string {
	if is3D {
		return fmt.Sprintf("%s|%d|3d", strings.ToLower(title), year)
	}
	return fmt.Sprintf("%s|%d", strings.ToLower(title), year)
}

// SeriesKey builds the settle key for one episode.
func SeriesKey(show, episodeID string) string {
	return strings.ToLower(show) + "|" + strings.ToUpper(episodeID)
}

// CandidateOf snapshots an entry as a settle candidate, copying the field map
// so later mutation of the entry cannot corrupt the stored winner.
func CandidateOf(e *entry.Entry) Candidate {
	q, _ := e.Quality()
	fields := make(map[string]any, len(e.Fields))
	for k, v := range e.Fields {
		fields[k] = v
	}
	return Candidate{URL: e.URL, Title: e.Title, Quality: q, Fields: fields}
}

// Rebuild reconstructs an entry from a stored winner, for downloading a
// release that has since left the feed. The quality struct is re-applied
// explicitly because the field map round-trips through JSON as plain values.
func (c Candidate) Rebuild() *entry.Entry {
	e := entry.New(c.Title, c.URL)
	for k, v := range c.Fields {
		if k == entry.FieldQuality {
			continue // restored below as a typed value
		}
		e.Fields[k] = v
	}
	e.SetQuality(c.Quality)
	return e
}
