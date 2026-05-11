package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/brunoga/pipeliner/plugins/filter/dedup"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/quality"

	"github.com/brunoga/pipeliner/internal/entry"
)

func switchableRSSServer(t *testing.T) (*httptest.Server, func([]rssItem)) {
	t.Helper()
	var current string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, current) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	set := func(items []rssItem) {
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0"?>`)
		sb.WriteString(`<rss version="2.0"><channel><title>Test</title>`)
		for _, it := range items {
			fmt.Fprintf(&sb, `<item><title>%s</title><link>%s</link></item>`, it.title, it.link)
		}
		sb.WriteString(`</channel></rss>`)
		current = sb.String()
	}
	return srv, set
}

func TestSeriesInRunDedup(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/bb-720p"},
		{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/bb-1080p"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src    = input("rss", url=%q)
series = process("series", from_=src, static=["Breaking Bad"])
q      = process("metainfo_quality", from_=series)
dd     = process("dedup", from_=q)
output("print", from_=dd)
pipeline("t")
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 1)
	if accepted := acceptedEntries(res.entries); len(accepted) != 1 || !strings.Contains(accepted[0].Title, "1080p") {
		t.Errorf("expected 1080p to survive dedup, got: %v", entryTitles(acceptedEntries(res.entries)))
	}
}

func TestMoviesInRunDedup(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Inception.2010.720p.HDTV", "http://example.com/inception-720p"},
		{"Inception.2010.1080p.BluRay", "http://example.com/inception-1080p"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src    = input("rss", url=%q)
movies = process("movies", from_=src, static=["Inception"])
q      = process("metainfo_quality", from_=movies)
dd     = process("dedup", from_=q)
output("print", from_=dd)
pipeline("t")
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 1)
	if accepted := acceptedEntries(res.entries); len(accepted) != 1 || !strings.Contains(accepted[0].Title, "1080p") {
		t.Errorf("expected 1080p to survive dedup, got: %v", entryTitles(acceptedEntries(res.entries)))
	}
}

func TestSeriesUpgradeAcrossRuns(t *testing.T) {
	srv, set := switchableRSSServer(t)

	tk := buildTask(t, fmt.Sprintf(`
src    = input("rss", url=%q)
series = process("series", from_=src, static=["Breaking Bad"])
output("print", from_=series)
pipeline("t")
`, srv.URL))

	set([]rssItem{{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/bb-720p"}})
	run(t, tk).assertAccepted(t, 1)

	set([]rssItem{{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/bb-1080p"}})
	run(t, tk).assertAccepted(t, 1)

	run(t, tk).assertRejected(t, 1)
}

func TestMoviesUpgradeAcrossRuns(t *testing.T) {
	srv, set := switchableRSSServer(t)

	tk := buildTask(t, fmt.Sprintf(`
src    = input("rss", url=%q)
movies = process("movies", from_=src, static=["Inception"])
output("print", from_=movies)
pipeline("t")
`, srv.URL))

	set([]rssItem{{"Inception.2010.720p.HDTV", "http://example.com/inception-720p"}})
	run(t, tk).assertAccepted(t, 1)

	set([]rssItem{{"Inception.2010.1080p.BluRay", "http://example.com/inception-1080p"}})
	run(t, tk).assertAccepted(t, 1)

	run(t, tk).assertRejected(t, 1)
}

func TestSeriesProperUpgradeAcrossRuns(t *testing.T) {
	srv, set := switchableRSSServer(t)

	tk := buildTask(t, fmt.Sprintf(`
src    = input("rss", url=%q)
series = process("series", from_=src, static=["Breaking Bad"])
output("print", from_=series)
pipeline("t")
`, srv.URL))

	set([]rssItem{{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/bb-720p"}})
	run(t, tk).assertAccepted(t, 1)

	set([]rssItem{{"Breaking.Bad.S01E01.PROPER.720p.HDTV", "http://example.com/bb-proper"}})
	run(t, tk).assertAccepted(t, 1)
}

func TestMoviesProperUpgradeAcrossRuns(t *testing.T) {
	srv, set := switchableRSSServer(t)

	tk := buildTask(t, fmt.Sprintf(`
src    = input("rss", url=%q)
movies = process("movies", from_=src, static=["Inception"])
output("print", from_=movies)
pipeline("t")
`, srv.URL))

	set([]rssItem{{"Inception.2010.720p.HDTV", "http://example.com/inception-720p"}})
	run(t, tk).assertAccepted(t, 1)

	set([]rssItem{{"Inception.2010.PROPER.720p.HDTV", "http://example.com/inception-proper"}})
	run(t, tk).assertAccepted(t, 1)
}

func TestSeriesDowngradeRejected(t *testing.T) {
	srv, set := switchableRSSServer(t)

	tk := buildTask(t, fmt.Sprintf(`
src    = input("rss", url=%q)
series = process("series", from_=src, static=["Breaking Bad"])
output("print", from_=series)
pipeline("t")
`, srv.URL))

	set([]rssItem{{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/bb-1080p"}})
	run(t, tk).assertAccepted(t, 1)

	set([]rssItem{{"Breaking.Bad.S01E01.PROPER.720p.HDTV", "http://example.com/bb-proper-720p"}})
	run(t, tk).assertRejected(t, 1)
}

func TestMoviesDowngradeRejected(t *testing.T) {
	srv, set := switchableRSSServer(t)

	tk := buildTask(t, fmt.Sprintf(`
src    = input("rss", url=%q)
movies = process("movies", from_=src, static=["Inception"])
output("print", from_=movies)
pipeline("t")
`, srv.URL))

	set([]rssItem{{"Inception.2010.1080p.BluRay", "http://example.com/inception-1080p"}})
	run(t, tk).assertAccepted(t, 1)

	set([]rssItem{{"Inception.2010.PROPER.720p.HDTV", "http://example.com/inception-proper-720p"}})
	run(t, tk).assertRejected(t, 1)
}

func acceptedEntries(entries []*entry.Entry) []*entry.Entry {
	var out []*entry.Entry
	for _, e := range entries {
		if e.IsAccepted() {
			out = append(out, e)
		}
	}
	return out
}

func entryTitles(entries []*entry.Entry) []string {
	titles := make([]string, len(entries))
	for i, e := range entries {
		titles[i] = e.Title
	}
	return titles
}
