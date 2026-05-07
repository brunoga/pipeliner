// Package integration runs end-to-end pipeline tests using real plugins,
// mock HTTP servers, and in-memory SQLite stores.
package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/config"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/task"

	// Register all plugins under test.
	_ "github.com/brunoga/pipeliner/plugins/filter/accept_all"
	_ "github.com/brunoga/pipeliner/plugins/filter/condition"
	_ "github.com/brunoga/pipeliner/plugins/filter/content"
	_ "github.com/brunoga/pipeliner/plugins/filter/exists"
	_ "github.com/brunoga/pipeliner/plugins/filter/list_match"
	_ "github.com/brunoga/pipeliner/plugins/filter/movies"
	_ "github.com/brunoga/pipeliner/plugins/filter/premiere"
	_ "github.com/brunoga/pipeliner/plugins/filter/quality"
	_ "github.com/brunoga/pipeliner/plugins/filter/regexp"
	_ "github.com/brunoga/pipeliner/plugins/filter/require"
	_ "github.com/brunoga/pipeliner/plugins/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/filter/series"
	_ "github.com/brunoga/pipeliner/plugins/filter/torrentalive"
	_ "github.com/brunoga/pipeliner/plugins/filter/trakt"
	_ "github.com/brunoga/pipeliner/plugins/filter/tvdb"
	_ "github.com/brunoga/pipeliner/plugins/filter/upgrade"
	_ "github.com/brunoga/pipeliner/plugins/from/jackett"
	_ "github.com/brunoga/pipeliner/plugins/from/rss"
	_ "github.com/brunoga/pipeliner/plugins/from/trakt"
	_ "github.com/brunoga/pipeliner/plugins/from/tvdb"
	_ "github.com/brunoga/pipeliner/plugins/input/discover"
	_ "github.com/brunoga/pipeliner/plugins/input/filesystem"
	_ "github.com/brunoga/pipeliner/plugins/input/html"
	_ "github.com/brunoga/pipeliner/plugins/input/rss"
	_ "github.com/brunoga/pipeliner/plugins/input/search/jackett"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/magnet"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/quality"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/series"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/trakt"
	_ "github.com/brunoga/pipeliner/plugins/modify/pathfmt"
	_ "github.com/brunoga/pipeliner/plugins/modify/pathscrub"
	_ "github.com/brunoga/pipeliner/plugins/modify/set"
	_ "github.com/brunoga/pipeliner/plugins/output/exec"
	_ "github.com/brunoga/pipeliner/plugins/output/list_add"
	_ "github.com/brunoga/pipeliner/plugins/output/print"
)

// ---------- helpers ----------

func rssServer(t *testing.T, items []rssItem) *httptest.Server {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?>`)
	sb.WriteString(`<rss version="2.0"><channel><title>Test</title>`)
	for _, it := range items {
		fmt.Fprintf(&sb,
			`<item><title>%s</title><link>%s</link></item>`,
			it.title, it.link,
		)
	}
	sb.WriteString(`</channel></rss>`)
	body := sb.String()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, body) //nolint:errcheck
	}))
}

type rssItem struct {
	title string
	link  string
}

// buildTask parses cfgYAML and returns the single task it defines.
// Plugin instances (and their stores) are created once here and reused
// across multiple Run calls — exactly as the daemon does between cycles.
// An in-memory SQLite store is used so tests are isolated and leave no files.
func buildTask(t *testing.T, cfgYAML string) *task.Task {
	t.Helper()
	return buildTaskWithDB(t, cfgYAML, nil)
}

func buildTaskWithDB(t *testing.T, cfgYAML string, db *store.SQLiteStore) *task.Task {
	t.Helper()
	if db == nil {
		var err error
		db, err = store.OpenSQLite(":memory:")
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { db.Close() })
	}
	cfg, err := config.ParseBytes([]byte(cfgYAML))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	tasks, err := config.BuildTasks(cfg, db, nil)
	if err != nil {
		t.Fatalf("build tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	return tasks[0]
}

func run(t *testing.T, tk *task.Task) *result {
	t.Helper()
	res, err := tk.Run(context.Background())
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	return &result{res.Accepted, res.Rejected, res.Entries}
}

// buildAndRun is a convenience wrapper for single-cycle tests.
func buildAndRun(t *testing.T, cfgYAML string) *result {
	t.Helper()
	return run(t, buildTask(t, cfgYAML))
}

// buildAndRunWithDB runs a single task using the provided shared store.
func buildAndRunWithDB(t *testing.T, cfgYAML string, db *store.SQLiteStore) *result {
	t.Helper()
	return run(t, buildTaskWithDB(t, cfgYAML, db))
}

type result struct {
	accepted int
	rejected int
	entries  []*entry.Entry
}

func (r *result) assertAccepted(t *testing.T, n int) {
	t.Helper()
	if r.accepted != n {
		t.Errorf("accepted: got %d, want %d", r.accepted, n)
	}
}

func (r *result) assertRejected(t *testing.T, n int) {
	t.Helper()
	if r.rejected != n {
		t.Errorf("rejected: got %d, want %d", r.rejected, n)
	}
}

// ---------- tests ----------

func TestRSSToRegexpFilter(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Linux Kernel 6.8 Released", "http://example.com/1"},
		{"Windows 12 Announced", "http://example.com/2"},
		{"Open Source AI Tools", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    regexp:
      accept:
        - '(?i)linux|open.?source'
    print:
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 0) // unmatched entries left undecided, not rejected
}

func TestSeenDeduplication(t *testing.T) {
	// The daemon builds each task once and calls Run() repeatedly.
	// Plugin instances — including the seen store — are shared across cycles.
	// Using :memory: here mirrors that exactly: one connection, state retained.
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
		{"Breaking.Bad.S01E02.720p.HDTV", "http://example.com/2"},
	})
	defer srv.Close()

	tk := buildTask(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    seen:
    regexp:
      accept:
        - '.+'
    print:
`, srv.URL))

	// First cycle: both entries accepted and learned.
	run(t, tk).assertAccepted(t, 2)

	// Second cycle: both rejected as already seen.
	r2 := run(t, tk)
	r2.assertAccepted(t, 0)
	r2.assertRejected(t, 2)
}

func TestSeriesFilterAcceptsKnownShow(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
		{"Breaking.Bad.S01E02.720p.HDTV", "http://example.com/2"},
		{"Some.Other.Show.S01E01.720p.HDTV", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    series:
      shows:
        - "Breaking Bad"
    print:
`, srv.URL))

	res.assertAccepted(t, 2) // both BB episodes accepted
	res.assertRejected(t, 0) // unknown show left undecided, not rejected
}

func TestSeriesSeenAcrossCycles(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	tk := buildTask(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    series:
      shows:
        - "Breaking Bad"
    print:
`, srv.URL))

	run(t, tk).assertAccepted(t, 1) // first cycle: accepted
	run(t, tk).assertRejected(t, 1) // second cycle: rejected as already seen
}

func TestQualityFilterRejectsBelow(t *testing.T) {
	// The quality filter rejects entries below the floor; matching entries
	// are left undecided for downstream filters/outputs to decide.
	srv := rssServer(t, []rssItem{
		{"Movie.2019.1080p.BluRay.x264", "http://example.com/1"},
		{"Movie.2019.720p.HDTV", "http://example.com/2"},
		{"Movie.2019.480p.DVDRip", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    quality:
      min: 720p
    print:
`, srv.URL))

	res.assertRejected(t, 1) // only 480p rejected
	// 1080p and 720p are undecided (not accepted, since no accept filter ran)
	if res.accepted != 0 {
		t.Errorf("quality filter should not accept entries, got %d accepted", res.accepted)
	}
}

func TestQualityFilterWithAccept(t *testing.T) {
	// Combining quality (rejects) with regexp (accepts the remainder) gives
	// a decisive pipeline: above-floor entries are accepted, below rejected.
	srv := rssServer(t, []rssItem{
		{"Movie.2019.1080p.BluRay.x264", "http://example.com/1"},
		{"Movie.2019.720p.HDTV", "http://example.com/2"},
		{"Movie.2019.480p.DVDRip", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    quality:
      min: 720p
    regexp:
      accept:
        - '.+'
    print:
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 1)
}

func TestMetainfoQualityAnnotates(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay.x264", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_quality:
    print:
`, srv.URL))

	if len(res.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(res.entries))
	}
	e := res.entries[0]
	if v := e.GetString("video_resolution"); v != "1080p" {
		t.Errorf("resolution: got %q, want 1080p", v)
	}
	if v := e.GetString("video_source"); v != "BluRay" {
		t.Errorf("source: got %q, want BluRay", v)
	}
	if v := e.GetString("codec"); v != "H264" {
		t.Errorf("codec: got %q, want H264", v)
	}
}

func TestMetainfoSeriesAnnotates(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S02E05.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_series:
    print:
`, srv.URL))

	if len(res.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(res.entries))
	}
	e := res.entries[0]
	if v := e.GetString("title"); v != "Breaking Bad" {
		t.Errorf("title: got %q", v)
	}
	if v := e.GetInt("series_season"); v != 2 {
		t.Errorf("season: got %d", v)
	}
	if v := e.GetString("series_episode_id"); v != "S02E05" {
		t.Errorf("episode_id: got %q", v)
	}
}

func TestConditionFilter(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay", "http://example.com/1"},
		{"Show.S01E02.720p.HDTV", "http://example.com/2"},
		{"Show.S01E03.480p.DVDRip", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_quality:
    condition:
      accept: '{{ne .video_resolution ""}}'
      reject: '{{eq .video_resolution "480p"}}'
    print:
`, srv.URL))

	res.assertAccepted(t, 2) // 1080p and 720p accepted
	res.assertRejected(t, 1) // 480p rejected
}

func TestVariableSubstitutionInConfig(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
variables:
  feed: %q
tasks:
  t:
    rss:
      url: "{$ feed $}"
    print:
`, srv.URL))

	if len(res.entries) != 1 {
		t.Errorf("expected 1 entry via variable-substituted URL, got %d", len(res.entries))
	}
}

func TestSetModify(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"My.Show.S01E01.720p", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    set:
      category: tv
      label: '{{.Title}}'
    print:
`, srv.URL))

	if len(res.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(res.entries))
	}
	e := res.entries[0]
	if v := e.GetString("category"); v != "tv" {
		t.Errorf("category: got %q", v)
	}
	if v := e.GetString("label"); v != "My.Show.S01E01.720p" {
		t.Errorf("label: got %q", v)
	}
}

func TestPathfmtModify(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S02E05.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_series:
    pathfmt:
      path: '/tv/{{.title}}/S{{printf "%%02d" .series_season}}'
    print:
`, srv.URL))

	if len(res.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(res.entries))
	}
	e := res.entries[0]
	if v := e.GetString("download_path"); v != "/tv/Breaking Bad/S02" {
		t.Errorf("download_path: got %q", v)
	}
}

func TestConfigCheck(t *testing.T) {
	cfg, err := config.ParseBytes([]byte(`
tasks:
  t:
    rss:
      url: "http://example.com/rss"
    print:
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := config.Validate(cfg)
	if len(errs) != 0 {
		t.Errorf("expected no validation errors, got: %v", errs)
	}
}

func TestConfigCheckUnknownPlugin(t *testing.T) {
	cfg, err := config.ParseBytes([]byte(`
tasks:
  t:
    no-such-plugin:
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := config.Validate(cfg)
	if len(errs) == 0 {
		t.Error("expected validation error for unknown plugin")
	}
}

func TestAllPluginsRegistered(t *testing.T) {
	want := []string{
		"rss", "html", "filesystem", "discover", "trakt_list", "tvdb_favorites",
		"seen", "regexp", "quality", "exists", "series", "movies", "condition",
		"require", "content", "premiere", "torrent_alive", "upgrade",
		"trakt", "tvdb", "accept_all", "list_match",
		"set", "pathfmt", "pathscrub",
		"print", "exec", "list_add",
		"metainfo_quality", "metainfo_series", "metainfo_trakt", "metainfo_magnet",
		"rss_search", "jackett",
	}
	for _, name := range want {
		if _, ok := plugin.Lookup(name); !ok {
			t.Errorf("plugin %q not registered", name)
		}
	}
}

func TestAcceptAll(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Article One", "http://example.com/1"},
		{"Article Two", "http://example.com/2"},
		{"Article Three", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    accept_all:
    print:
`, srv.URL))

	res.assertAccepted(t, 3)
	res.assertRejected(t, 0)
}

func TestAcceptAllLeavesRejectedAlone(t *testing.T) {
	// accept_all should not un-reject entries rejected by an earlier filter.
	srv := rssServer(t, []rssItem{
		{"Linux Kernel 6.8", "http://example.com/1"},
		{"Windows 12 Announced", "http://example.com/2"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    regexp:
      reject:
        - '(?i)windows'
    accept_all:
    print:
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 1)
}

func TestListAddAndMatch(t *testing.T) {
	// Both tasks share the same store so list_add and list_match see the same data.
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	srvAdd := rssServer(t, []rssItem{
		{"Breaking Bad", "http://example.com/bb"},
		{"Better Call Saul", "http://example.com/bcs"},
	})
	defer srvAdd.Close()

	// Task A: accept everything and add to a list.
	buildAndRunWithDB(t, fmt.Sprintf(`
tasks:
  add-task:
    rss:
      url: %q
    accept_all:
    list_add:
      list: watchlist
`, srvAdd.URL), db)

	// Task B: feed with three entries; only the two in the list should be accepted.
	srvMatch := rssServer(t, []rssItem{
		{"Breaking Bad", "http://example.com/bb2"},
		{"Better Call Saul", "http://example.com/bcs2"},
		{"Some Unknown Show", "http://example.com/unk"},
	})
	defer srvMatch.Close()

	res := buildAndRunWithDB(t, fmt.Sprintf(`
tasks:
  match-task:
    rss:
      url: %q
    list_match:
      list: watchlist
    print:
`, srvMatch.URL), db)

	res.assertAccepted(t, 2)
	res.assertRejected(t, 1)
}

func TestConditionInfixSyntax(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay", "http://example.com/1"},
		{"Show.S01E02.720p.HDTV", "http://example.com/2"},
		{"Show.S01E03.480p.DVDRip", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_quality:
    condition:
      accept: 'video_resolution != ""'
      reject: 'video_resolution == "480p"'
    print:
`, srv.URL))

	res.assertAccepted(t, 2) // 1080p and 720p accepted
	res.assertRejected(t, 1) // 480p rejected
}

func TestConditionContainsOperator(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay.x264", "http://example.com/1"},
		{"Show.S01E02.720p.HDTV.x265", "http://example.com/2"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_quality:
    condition:
      accept: 'video_source contains "BluRay"'
    print:
`, srv.URL))

	res.assertAccepted(t, 1) // only BluRay entry
	res.assertRejected(t, 0) // HDTV left undecided
}

func TestPathfmtNewSyntax(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S02E05.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_series:
    pathfmt:
      path: '/tv/{title}/Season {series_season:02d}'
    print:
`, srv.URL))

	if len(res.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(res.entries))
	}
	if v := res.entries[0].GetString("download_path"); v != "/tv/Breaking Bad/Season 02" {
		t.Errorf("download_path: got %q, want /tv/Breaking Bad/Season 02", v)
	}
}

func TestSetNewSyntax(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"My.Show.S01E01.720p", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    set:
      category: tv
      label: '{raw_title}'
    print:
`, srv.URL))

	if len(res.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(res.entries))
	}
	e := res.entries[0]
	if v := e.GetString("category"); v != "tv" {
		t.Errorf("category: got %q", v)
	}
	if v := e.GetString("label"); v != "My.Show.S01E01.720p" {
		t.Errorf("label: got %q", v)
	}
}

func TestTemplateInheritance(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
		{"Breaking.Bad.S01E02.480p.DVDRip", "http://example.com/2"},
	})
	defer srv.Close()

	// The task inherits quality: min: 720p from the base template.
	res := buildAndRun(t, fmt.Sprintf(`
templates:
  hd-only:
    quality:
      min: 720p
    regexp:
      accept:
        - '.+'

tasks:
  t:
    template: hd-only
    rss:
      url: %q
    print:
`, srv.URL))

	res.assertAccepted(t, 1) // only 720p entry passes quality gate
	res.assertRejected(t, 1)
}

func TestMultipleTemplates(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/1"},
		{"Breaking.Bad.S01E02.480p.DVDRip", "http://example.com/2"},
		{"Some.Other.Show.S01E01.1080p.BluRay", "http://example.com/3"},
	})
	defer srv.Close()

	// Template hd-base provides the quality floor; template bb-only provides
	// the series acceptance. Both are merged into the task.
	res := buildAndRun(t, fmt.Sprintf(`
templates:
  hd-base:
    quality:
      min: 720p
  bb-only:
    series:
      shows:
        - "Breaking Bad"

tasks:
  t:
    template:
      - hd-base
      - bb-only
    rss:
      url: %q
    print:
`, srv.URL))

	res.assertAccepted(t, 1) // only BB 1080p: passes quality AND series filter
	res.assertRejected(t, 1) // BB 480p rejected by quality
	// Other.Show.S01E01 left undecided (not in series list, not rejected by quality)
}

func TestRegexpPerPatternFrom(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay", "http://example.com/1"},
		{"Show.S01E02.720p.HDTV", "http://example.com/2"},
	})
	defer srv.Close()

	// Reject entries where the source field matches "BluRay" using per-pattern from.
	res := buildAndRun(t, fmt.Sprintf(`
tasks:
  t:
    rss:
      url: %q
    metainfo_quality:
    regexp:
      reject:
        - pattern: 'BluRay'
          from: video_source
      accept:
        - '.+'
    print:
`, srv.URL))

	res.assertAccepted(t, 1) // HDTV accepted
	res.assertRejected(t, 1) // BluRay rejected
}
