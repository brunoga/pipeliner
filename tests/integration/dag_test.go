package integration

// dag_test.go exercises DAG-style pipelines (input/process/output/pipeline)
// end-to-end using real plugins, mock HTTP servers, and in-memory SQLite.
//
// The existing buildAndRun / buildTask / run helpers work unchanged because
// config.BuildTasks returns both linear tasks and DAG pipelines as *task.Task.

import (
	"fmt"
	"testing"

	"github.com/brunoga/pipeliner/internal/config"
	_ "github.com/brunoga/pipeliner/plugins/filter/dedup"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/quality"
)

func parseConfig(t *testing.T, src string) (*config.Config, error) {
	t.Helper()
	return config.ParseBytes([]byte(src))
}

func validateConfig(t *testing.T, cfg *config.Config) []error {
	t.Helper()
	return config.Validate(cfg)
}

// TestDAG_BasicPipeline is the simplest end-to-end DAG: one source → one sink.
func TestDAG_BasicPipeline(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Article One", "http://example.com/1"},
		{"Article Two", "http://example.com/2"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src = input("rss", url=%q)
output("print", from_=src)
pipeline("p")
`, srv.URL))

	res.assertAccepted(t, 0) // print sink doesn't accept — entries stay Undecided
	if len(res.entries) != 2 {
		t.Errorf("want 2 entries, got %d", len(res.entries))
	}
}

// TestDAG_FilterChain tests a source → processor → processor → sink chain.
// regexp with both accept and reject patterns explicitly rejects non-matching
// entries; accept_all then accepts what remains.
func TestDAG_FilterChain(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Linux Kernel 6.8 Released", "http://example.com/1"},
		{"Windows 12 Announced", "http://example.com/2"},
		{"Open Source AI Tools", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src      = input("rss", url=%q)
filtered = process("regexp", from_=src,
                   accept=["(?i)linux|open.?source"],
                   reject=["(?i)windows"])
accepted = process("accept_all", from_=filtered)
output("print", from_=accepted)
pipeline("p")
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 1)
}

// TestDAG_MetainfoThenFilter tests that a metainfo processor enriches fields
// that a downstream filter can read.
func TestDAG_MetainfoThenFilter(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Show.S01E01.1080p.BluRay.x264", "http://example.com/1"},
		{"Show.S01E02.720p.HDTV", "http://example.com/2"},
		{"Show.S01E03.480p.DVDRip", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src     = input("rss", url=%q)
quality = process("metainfo_quality", from_=src)
flt     = process("quality", from_=quality, min="720p")
acc     = process("accept_all", from_=flt)
output("print", from_=acc)
pipeline("p")
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 1)
}

// TestDAG_MetainfoAnnotatesFields verifies fields set by a processor are
// visible on the entry that reaches the sink.
func TestDAG_MetainfoAnnotatesFields(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S02E05.720p.BluRay.x264", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src    = input("rss", url=%q)
series = process("metainfo_series", from_=src)
qual   = process("metainfo_quality", from_=series)
output("print", from_=qual)
pipeline("p")
`, srv.URL))

	if len(res.entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(res.entries))
	}
	e := res.entries[0]
	if v := e.GetString("title"); v != "Breaking Bad" {
		t.Errorf("title: got %q, want Breaking Bad", v)
	}
	if v := e.GetInt("series_season"); v != 2 {
		t.Errorf("series_season: got %d, want 2", v)
	}
	if v := e.GetString("video_resolution"); v != "720p" {
		t.Errorf("video_resolution: got %q, want 720p", v)
	}
}

// TestDAG_Merge verifies that two sources are merged and deduplicated by URL.
func TestDAG_Merge(t *testing.T) {
	srv1 := rssServer(t, []rssItem{
		{"Article A", "http://example.com/a"},
		{"Article B", "http://example.com/b"},
	})
	defer srv1.Close()

	srv2 := rssServer(t, []rssItem{
		{"Article B", "http://example.com/b"}, // duplicate URL — deduped at merge
		{"Article C", "http://example.com/c"},
	})
	defer srv2.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src1 = input("rss", url=%q)
src2 = input("rss", url=%q)
acc  = process("accept_all", from_=merge(src1, src2))
output("print", from_=acc)
pipeline("p")
`, srv1.URL, srv2.URL))

	// 4 raw entries across 2 feeds; 1 duplicate URL deduped → 3 accepted.
	// res.Total counts raw source outputs (4); res.Accepted counts unique entries
	// that actually reached downstream nodes (3).
	res.assertAccepted(t, 3)
}

// TestDAG_FanOut verifies that a single processor output can feed two sinks
// and each receives all entries independently.
func TestDAG_FanOut(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Article One", "http://example.com/1"},
		{"Article Two", "http://example.com/2"},
	})
	defer srv.Close()

	// Two print sinks both read from the same processor.
	// We verify neither errors and entries flow through.
	res := buildAndRun(t, fmt.Sprintf(`
src = input("rss", url=%q)
acc = process("accept_all", from_=src)
output("print", from_=acc)
output("print", from_=acc)
pipeline("p")
`, srv.URL))

	res.assertAccepted(t, 2)
}

// TestDAG_SeenAcrossRuns verifies that seen rejects on the second run.
func TestDAG_SeenAcrossRuns(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.720p", "http://example.com/1"},
		{"Breaking.Bad.S01E02.720p", "http://example.com/2"},
	})
	defer srv.Close()

	cfgStar := fmt.Sprintf(`
src  = input("rss", url=%q)
seen = process("seen", from_=src)
acc  = process("accept_all", from_=seen)
output("print", from_=acc)
pipeline("p")
`, srv.URL)

	tk := buildTask(t, cfgStar)
	run(t, tk).assertAccepted(t, 2)

	r2 := run(t, tk)
	r2.assertAccepted(t, 0)
	r2.assertRejected(t, 2)
}

// TestDAG_DedupPlugin verifies the dedup processor removes lower-quality
// duplicates of the same episode.
func TestDAG_DedupPlugin(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S01E01.1080p.BluRay", "http://example.com/1"},
		{"Breaking.Bad.S01E01.720p.HDTV", "http://example.com/2"},
		{"Breaking.Bad.S01E02.720p.HDTV", "http://example.com/3"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src    = input("rss", url=%q)
series = process("metainfo_series", from_=src)
qual   = process("metainfo_quality", from_=series)
acc    = process("accept_all", from_=qual)
dd     = process("dedup", from_=acc)
output("print", from_=dd)
pipeline("p")
`, srv.URL))

	res.assertAccepted(t, 2)
	res.assertRejected(t, 1)
}

// TestDAG_TwoPipelines verifies that two pipeline() blocks in the same file
// build two independent graphs.
func TestDAG_TwoPipelines(t *testing.T) {
	srv := rssServer(t, []rssItem{{"Article One", "http://example.com/1"}})
	defer srv.Close()

	cfgStar := fmt.Sprintf(`
src1 = input("rss", url=%q)
output("print", from_=src1)
pipeline("p1")

src2 = input("rss", url=%q)
output("print", from_=src2)
pipeline("p2")
`, srv.URL, srv.URL)

	cfg, err := parseConfig(t, cfgStar)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Graphs) != 2 {
		t.Errorf("want 2 DAG graphs, got %d", len(cfg.Graphs))
	}
}

// TestDAG_Validate_UnknownPlugin verifies Validate catches unknown plugins in
// DAG graphs.
func TestDAG_Validate_UnknownPlugin(t *testing.T) {
	cfg, err := parseConfig(t, `
src = input("rss", url="http://example.com/rss")
output("no_such_plugin", from_=src)
pipeline("p")
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	errs := validateConfig(t, cfg)
	if len(errs) == 0 {
		t.Error("expected validation error for unknown plugin, got none")
	}
}

// TestDAG_Validate_FieldRequirement verifies that the validator catches a
// filter that requires video_quality when no upstream produces it.
func TestDAG_Validate_FieldRequirement(t *testing.T) {
	cfg, err := parseConfig(t, `
src = input("rss", url="http://example.com/rss")
flt = process("quality", from_=src, min="720p")
output("print", from_=flt)
pipeline("p")
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	errs := validateConfig(t, cfg)
	if len(errs) == 0 {
		t.Error("expected validation error for missing video_quality field, got none")
	}
}

// TestDAG_Validate_FieldRequirement_SatisfiedByUpstream verifies no errors
// when metainfo_quality is correctly placed before the quality filter.
func TestDAG_Validate_FieldRequirement_SatisfiedByUpstream(t *testing.T) {
	cfg, err := parseConfig(t, `
src     = input("rss", url="http://example.com/rss")
quality = process("metainfo_quality", from_=src)
flt     = process("quality", from_=quality, min="720p")
output("print", from_=flt)
pipeline("p")
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if errs := validateConfig(t, cfg); len(errs) != 0 {
		t.Errorf("expected no validation errors, got: %v", errs)
	}
}

// TestDAG_PathfmtInChain tests that pathfmt as a DAG processor writes the
// expected field.
func TestDAG_PathfmtInChain(t *testing.T) {
	srv := rssServer(t, []rssItem{
		{"Breaking.Bad.S02E05.720p.HDTV", "http://example.com/1"},
	})
	defer srv.Close()

	res := buildAndRun(t, fmt.Sprintf(`
src    = input("rss", url=%q)
series = process("metainfo_series", from_=src)
fmt    = process("pathfmt", from_=series,
                 path="/tv/{title}/Season {series_season:02d}",
                 field="download_path")
output("print", from_=fmt)
pipeline("p")
`, srv.URL))

	if len(res.entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(res.entries))
	}
	if v := res.entries[0].GetString("download_path"); v != "/tv/Breaking Bad/Season 02" {
		t.Errorf("download_path: got %q, want /tv/Breaking Bad/Season 02", v)
	}
}

// TestDAG_Validate_Cycle verifies that a cyclic graph is rejected at parse
// time (AddNode errors on missing upstream).
func TestDAG_Validate_Cycle(t *testing.T) {
	// We cannot express a true cycle in the Starlark API (forward references
	// are impossible since handles are returned by value). Instead, verify
	// that referencing an unknown upstream node ID is caught.
	_, err := parseConfig(t, `
process("seen", from_="nonexistent_handle")
pipeline("p")
`)
	if err == nil {
		t.Error("expected parse error for invalid from_ argument, got nil")
	}
}
