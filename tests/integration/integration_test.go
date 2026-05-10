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

func buildTask(t *testing.T, cfgStar string) *task.Task {
	t.Helper()
	return buildTaskWithDB(t, cfgStar, nil)
}

func buildTaskWithDB(t *testing.T, cfgStar string, db *store.SQLiteStore) *task.Task {
	t.Helper()
	if db == nil {
		var err error
		db, err = store.OpenSQLite(":memory:")
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { db.Close() })
	}
	cfg, err := config.ParseBytes([]byte(cfgStar))
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

func buildAndRun(t *testing.T, cfgStar string) *result {
	t.Helper()
	return run(t, buildTask(t, cfgStar))
}

func buildAndRunWithDB(t *testing.T, cfgStar string, db *store.SQLiteStore) *result {
	t.Helper()
	return run(t, buildTaskWithDB(t, cfgStar, db))
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
task("t", [
    plugin("rss", url=%q),
    plugin("regexp", accept=["(?i)linux|open.?source"]),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 0)
}

func TestSeenDeduplication(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
		{"Breaking.Bad.S01E02.720p.HDTV", "http://example.com/2"},
	})
	defer srv.Close()

	tk := buildTask(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("seen"),
    plugin("regexp", accept=[".+"]),
    plugin("print"),
])
`, srv.URL))

	run(t, tk).assertAccepted(t, 2)
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
task("t", [
    plugin("rss", url=%q),
    plugin("series", static=["Breaking Bad"]),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 1)
}

func TestSeriesSeenAcrossCycles(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	tk := buildTask(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("series", static=["Breaking Bad"]),
    plugin("print"),
])
`, srv.URL))

	run(t, tk).assertAccepted(t, 1)
	run(t, tk).assertRejected(t, 1)
}

func TestQualityFilterRejectsBelow(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Movie.2019.1080p.BluRay.x264", "http://example.com/1"},
		{"Movie.2019.720p.HDTV", "http://example.com/2"},
		{"Movie.2019.480p.DVDRip", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("quality", min="720p"),
    plugin("print"),
])
`, srv.URL))

	res.assertRejected(t, 1)
	if res.accepted != 0 {
		t.Errorf("quality filter should not accept entries, got %d accepted", res.accepted)
	}
}

func TestQualityFilterWithAccept(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Movie.2019.1080p.BluRay.x264", "http://example.com/1"},
		{"Movie.2019.720p.HDTV", "http://example.com/2"},
		{"Movie.2019.480p.DVDRip", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("quality", min="720p"),
    plugin("regexp", accept=[".+"]),
    plugin("print"),
])
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
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_quality"),
    plugin("print"),
])
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
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_series"),
    plugin("print"),
])
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

func TestEnrichedFieldUsedAsRequire(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
		{"Some.Show.S01E02.720p.HDTV", "http://example.com/2"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_series"),
    plugin("require", fields=["enriched"]),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 0)
	res.assertRejected(t, 2)
}

func TestConditionFilter(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay", "http://example.com/1"},
		{"Show.S01E02.720p.HDTV", "http://example.com/2"},
		{"Show.S01E03.480p.DVDRip", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_quality"),
    plugin("condition",
        accept='{{ne .video_resolution ""}}',
        reject='{{eq .video_resolution "480p"}}'),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 1)
}

func TestVariableSubstitutionInConfig(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	// Variables are just Starlark variables — no special syntax needed.
	res := buildAndRun(t, fmt.Sprintf(`
feed = %q
task("t", [
    plugin("rss", url=feed),
    plugin("print"),
])
`, srv.URL))

	if len(res.entries) != 1 {
		t.Errorf("expected 1 entry via variable URL, got %d", len(res.entries))
	}
}

func TestSetModify(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"My.Show.S01E01.720p", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("set", category="tv", label="{{.Title}}"),
    plugin("print"),
])
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
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_series"),
    plugin("pathfmt",
        path='/tv/{{.title}}/S{{printf "%%02d" .series_season}}',
        field="download_path"),
    plugin("print"),
])
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
task("t", [
    plugin("rss", url="http://example.com/rss"),
    plugin("print"),
])
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := config.Validate(cfg); len(errs) != 0 {
		t.Errorf("expected no validation errors, got: %v", errs)
	}
}

func TestConfigCheckUnknownPlugin(t *testing.T) {
	cfg, err := config.ParseBytes([]byte(`
task("t", [plugin("no-such-plugin")])
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := config.Validate(cfg); len(errs) == 0 {
		t.Error("expected validation error for unknown plugin")
	}
}

func TestAllPluginsRegistered(t *testing.T) {
	want := []string{
		"rss", "html", "filesystem", "discover", "trakt_list", "tvdb_favorites",
		"seen", "regexp", "quality", "exists", "series", "movies", "condition",
		"require", "content", "premiere", "torrent_alive", "upgrade",
		"trakt", "tvdb", "accept_all", "list_match",
		"set", "pathfmt",
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
task("t", [
    plugin("rss", url=%q),
    plugin("accept_all"),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 3)
	res.assertRejected(t, 0)
}

func TestAcceptAllLeavesRejectedAlone(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Linux Kernel 6.8", "http://example.com/1"},
		{"Windows 12 Announced", "http://example.com/2"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("regexp", reject=["(?i)windows"]),
    plugin("accept_all"),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 1)
}

func TestListAddAndMatch(t *testing.T) {
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

	buildAndRunWithDB(t, fmt.Sprintf(`
task("add-task", [
    plugin("rss", url=%q),
    plugin("accept_all"),
    plugin("list_add", list="watchlist"),
])
`, srvAdd.URL), db)

	srvMatch := rssServer(t, []rssItem{
		{"Breaking Bad", "http://example.com/bb2"},
		{"Better Call Saul", "http://example.com/bcs2"},
		{"Some Unknown Show", "http://example.com/unk"},
	})
	defer srvMatch.Close()

	res := buildAndRunWithDB(t, fmt.Sprintf(`
task("match-task", [
    plugin("rss", url=%q),
    plugin("list_match", list="watchlist"),
    plugin("print"),
])
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
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_quality"),
    plugin("condition",
        accept='video_resolution != ""',
        reject='video_resolution == "480p"'),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 1)
}

func TestConditionContainsOperator(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay.x264", "http://example.com/1"},
		{"Show.S01E02.720p.HDTV.x265", "http://example.com/2"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_quality"),
    plugin("condition", accept='video_source contains "BluRay"'),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 0)
}

func TestPathfmtNewSyntax(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S02E05.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_series"),
    plugin("pathfmt", path="/tv/{title}/Season {series_season:02d}", field="download_path"),
    plugin("print"),
])
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
task("t", [
    plugin("rss", url=%q),
    plugin("set", category="tv", label="{raw_title}"),
    plugin("print"),
])
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

	// Templates are Starlark functions.
	res := buildAndRun(t, fmt.Sprintf(`
def hd_only():
    return [
        plugin("quality", min="720p"),
        plugin("regexp", accept=[".+"]),
    ]

task("t", [plugin("rss", url=%q)] + hd_only() + [plugin("print")])
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 1)
}

func TestMultipleTemplates(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/1"},
		{"Breaking.Bad.S01E02.480p.DVDRip", "http://example.com/2"},
		{"Some.Other.Show.S01E01.1080p.BluRay", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
def hd_base():
    return [plugin("quality", min="720p")]

def bb_only():
    return [plugin("series", static=["Breaking Bad"])]

task("t", [plugin("rss", url=%q)] + hd_base() + bb_only() + [plugin("print")])
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 2)
}

func TestRegexpPerPatternFrom(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay", "http://example.com/1"},
		{"Show.S01E02.720p.HDTV", "http://example.com/2"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
task("t", [
    plugin("rss", url=%q),
    plugin("metainfo_quality"),
    plugin("regexp",
        reject=[{"pattern": "BluRay", "from": "video_source"}],
        accept=[".+"]),
    plugin("print"),
])
`, srv.URL))

	res.assertAccepted(t, 1)
	res.assertRejected(t, 1)
}
