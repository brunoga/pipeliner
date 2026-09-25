package settle

import (
	"fmt"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
)

// MovieBucketName and SeriesBucketName hold settle windows for every
// pipeline; keys are scoped by task name so the buckets can be shared.
const (
	MovieBucketName  = "movies_settle"
	SeriesBucketName = "series_settle"
)

// Keys are scoped by the task that recorded them. Unlike a download record —
// which is deliberately shared, because a file on disk is downloaded no
// matter which pipeline fetched it — a settle window is one pipeline's
// pending intent, and its winner is revived by injecting an entry directly
// into that pipeline's filter, skipping every upstream node. An unscoped key
// therefore let one pipeline's pending release be injected into another,
// past gates it had never passed: a 2D release recorded by a movies pipeline
// was revived inside a 3D pipeline whose quality spec had rejected it four
// times.

// MovieKey builds the settle key for a movie in one task. The title/year/3d
// portion matches the download tracker's key shape.
func MovieKey(task, title string, year int, is3D bool) string {
	if is3D {
		return fmt.Sprintf("%s|%s|%d|3d", task, strings.ToLower(title), year)
	}
	return fmt.Sprintf("%s|%s|%d", task, strings.ToLower(title), year)
}

// SeriesKey builds the settle key for one episode in one task.
func SeriesKey(task, show, episodeID string) string {
	return task + "|" + strings.ToLower(show) + "|" + strings.ToUpper(episodeID)
}

// TaskPrefix is the key prefix owned by one task.
func TaskPrefix(task string) string { return task + "|" }

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
