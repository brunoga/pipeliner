package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

// switchableRSSServer returns a server whose feed can be swapped between runs
// by calling the returned setter. The server is closed via t.Cleanup.
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

// ---------- in-run dedup ----------

func TestSeriesInRunDedup(t *testing.T) {
	// Two copies of the same episode in a single run — dedup should keep the
	// better quality (1080p BluRay) and reject the worse (720p HDTV).
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/bb-720p"},
		{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/bb-1080p"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_series:
    series:
      static:
        - "Breaking Bad"
    print:
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 1)
	if accepted := acceptedEntries(res.entries); len(accepted) != 1 || !strings.Contains(accepted[0].Title, "1080p") {
		t.Errorf("expected 1080p entry to survive dedup, got: %v", entryTitles(acceptedEntries(res.entries)))
	}
}

func TestMoviesInRunDedup(t *testing.T) {
	// Two copies of the same movie in a single run — dedup should keep the
	// better quality (1080p BluRay) and reject the worse (720p HDTV).
	srv := rssServer(t, []rssItem{
		{"Inception.2010.720p.HDTV", "http://example.com/inception-720p"},
		{"Inception.2010.1080p.BluRay", "http://example.com/inception-1080p"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    movies:
      static:
        - "Inception"
    print:
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 1)
	if accepted := acceptedEntries(res.entries); len(accepted) != 1 || !strings.Contains(accepted[0].Title, "1080p") {
		t.Errorf("expected 1080p entry to survive dedup, got: %v", entryTitles(acceptedEntries(res.entries)))
	}
}

// ---------- cross-run quality upgrade ----------

func TestSeriesUpgradeAcrossRuns(t *testing.T) {
	srv, set := switchableRSSServer(t)

	cfg := fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    series:
      static:
        - "Breaking Bad"
    print:
`, srv.URL)
	tk := buildTask(t, cfg)

	// Run 1: 720p accepted and recorded.
	set([]rssItem{{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/bb-720p"}})
	run(t, tk).assertAccepted(t, 1)

	// Run 2: 1080p is strictly better — accepted as quality upgrade.
	set([]rssItem{{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/bb-1080p"}})
	run(t, tk).assertAccepted(t, 1)

	// Run 3: 1080p again — no improvement over stored 1080p, rejected.
	run(t, tk).assertRejected(t, 1)
}

func TestMoviesUpgradeAcrossRuns(t *testing.T) {
	srv, set := switchableRSSServer(t)

	cfg := fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    movies:
      static:
        - "Inception"
    print:
`, srv.URL)
	tk := buildTask(t, cfg)

	// Run 1: 720p accepted and recorded.
	set([]rssItem{{"Inception.2010.720p.HDTV", "http://example.com/inception-720p"}})
	run(t, tk).assertAccepted(t, 1)

	// Run 2: 1080p is strictly better — accepted as quality upgrade.
	set([]rssItem{{"Inception.2010.1080p.BluRay", "http://example.com/inception-1080p"}})
	run(t, tk).assertAccepted(t, 1)

	// Run 3: 1080p again — no improvement, rejected.
	run(t, tk).assertRejected(t, 1)
}

// ---------- cross-run proper/repack upgrade ----------

func TestSeriesProperUpgradeAcrossRuns(t *testing.T) {
	srv, set := switchableRSSServer(t)

	cfg := fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    series:
      static:
        - "Breaking Bad"
    print:
`, srv.URL)
	tk := buildTask(t, cfg)

	// Run 1: 720p accepted and recorded.
	set([]rssItem{{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/bb-720p"}})
	run(t, tk).assertAccepted(t, 1)

	// Run 2: PROPER at same quality — accepted (fixes content issues even without resolution bump).
	set([]rssItem{{"Breaking.Bad.S01E01.PROPER.720p.HDTV", "http://example.com/bb-proper"}})
	run(t, tk).assertAccepted(t, 1)
}

func TestMoviesProperUpgradeAcrossRuns(t *testing.T) {
	srv, set := switchableRSSServer(t)

	cfg := fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    movies:
      static:
        - "Inception"
    print:
`, srv.URL)
	tk := buildTask(t, cfg)

	// Run 1: 720p accepted and recorded.
	set([]rssItem{{"Inception.2010.720p.HDTV", "http://example.com/inception-720p"}})
	run(t, tk).assertAccepted(t, 1)

	// Run 2: PROPER at same quality — accepted.
	set([]rssItem{{"Inception.2010.PROPER.720p.HDTV", "http://example.com/inception-proper"}})
	run(t, tk).assertAccepted(t, 1)
}

// ---------- cross-run downgrade rejected ----------

func TestSeriesDowngradeRejected(t *testing.T) {
	srv, set := switchableRSSServer(t)

	cfg := fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    series:
      static:
        - "Breaking Bad"
    print:
`, srv.URL)
	tk := buildTask(t, cfg)

	// Run 1: 1080p BluRay accepted.
	set([]rssItem{{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/bb-1080p"}})
	run(t, tk).assertAccepted(t, 1)

	// Run 2: PROPER 720p HDTV — lower quality (HDTV < BluRay), rejected even with PROPER tag.
	set([]rssItem{{"Breaking.Bad.S01E01.PROPER.720p.HDTV", "http://example.com/bb-proper-720p"}})
	run(t, tk).assertRejected(t, 1)
}

func TestMoviesDowngradeRejected(t *testing.T) {
	srv, set := switchableRSSServer(t)

	cfg := fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    movies:
      static:
        - "Inception"
    print:
`, srv.URL)
	tk := buildTask(t, cfg)

	// Run 1: 1080p BluRay accepted.
	set([]rssItem{{"Inception.2010.1080p.BluRay", "http://example.com/inception-1080p"}})
	run(t, tk).assertAccepted(t, 1)

	// Run 2: PROPER 720p HDTV — lower quality, rejected even with PROPER tag.
	set([]rssItem{{"Inception.2010.PROPER.720p.HDTV", "http://example.com/inception-proper-720p"}})
	run(t, tk).assertRejected(t, 1)
}

// ---------- helpers ----------

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
