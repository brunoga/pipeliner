package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"

	_ "github.com/brunoga/pipeliner/plugins/processor/filter/movies"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/premiere"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/quality"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/series"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/file"
	_ "github.com/brunoga/pipeliner/plugins/sink/print"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
)

// pluginNamesIn returns the set of plugin names used by nodes in g.
func pluginNamesIn(g *dag.Graph) []string {
	out := make([]string, 0, g.Len())
	for _, n := range g.Nodes() {
		out = append(out, n.PluginName)
	}
	return out
}

func TestMigrateLegacyQualityOnSeries(t *testing.T) {
	c := parseDAGOK(t, `
src    = input("rss", url="https://example.com/feed")
meta   = process("metainfo_file", upstream=src)
series = process("series", upstream=meta,
                 static=["Breaking Bad"], quality="720p+", tracking="strict")
output("print", upstream=series)
pipeline("legacy")
`)
	g := c.Graphs["legacy"]
	if g == nil {
		t.Fatal("graph missing")
	}
	names := pluginNamesIn(g)
	if !slices.Contains(names, "quality") {
		t.Errorf("legacy quality= should have inserted a quality node; nodes=%v", names)
	}

	// The series node config must no longer carry the quality key.
	for _, n := range g.Nodes() {
		if n.PluginName == "series" {
			if _, ok := n.Config["quality"]; ok {
				t.Error("series node still carries quality= after migration")
			}
		}
		if n.PluginName == "quality" {
			if spec, _ := n.Config["spec"].(string); spec != "720p+" {
				t.Errorf("inserted quality node spec: got %q, want %q", spec, "720p+")
			}
		}
	}

	// Warning must be surfaced via LoadWarnings.
	if len(c.LoadWarnings) == 0 {
		t.Fatal("expected a deprecation warning, got none")
	}
	found := false
	for _, w := range c.LoadWarnings {
		if strings.Contains(w.Error(), `"series"`) && strings.Contains(w.Error(), "quality") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected series quality deprecation warning; got %v", c.LoadWarnings)
	}
}

func TestMigrateLegacyQualityOnMovies(t *testing.T) {
	c := parseDAGOK(t, `
src    = input("rss", url="https://example.com/feed")
meta   = process("metainfo_file", upstream=src)
movies = process("movies", upstream=meta, static=["Inception"], quality="1080p+")
output("print", upstream=movies)
pipeline("legacy-movies")
`)
	g := c.Graphs["legacy-movies"]
	if !slices.Contains(pluginNamesIn(g), "quality") {
		t.Errorf("movies quality= should have inserted quality node; nodes=%v", pluginNamesIn(g))
	}
}

func TestMigrateLegacyQualityOnPremiere(t *testing.T) {
	c := parseDAGOK(t, `
src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)
prem = process("premiere", upstream=meta, quality="720p+")
output("print", upstream=prem)
pipeline("legacy-premiere")
`)
	g := c.Graphs["legacy-premiere"]
	if !slices.Contains(pluginNamesIn(g), "quality") {
		t.Errorf("premiere quality= should have inserted quality node; nodes=%v", pluginNamesIn(g))
	}
}

func TestMigrateTagsInjectedNode(t *testing.T) {
	c := parseDAGOK(t, `
src    = input("rss", url="https://example.com/feed")
meta   = process("metainfo_file", upstream=src)
series = process("series", upstream=meta, static=["x"], quality="720p+")
output("print", upstream=series)
pipeline("p")
`)
	g := c.Graphs["p"]
	var qualityNode, seriesNode *dag.Node
	for _, n := range g.Nodes() {
		switch n.PluginName {
		case "quality":
			qualityNode = n
		case "series":
			seriesNode = n
		}
	}
	if qualityNode == nil || seriesNode == nil {
		t.Fatal("missing expected nodes")
	}
	if qualityNode.AutoMigrated == "" {
		t.Error("injected quality node should carry AutoMigrated tag")
	}
	if seriesNode.AutoMigrated != "" {
		t.Errorf("user-authored series node should not be tagged; got %q", seriesNode.AutoMigrated)
	}
}

func TestMigrateRewiresUpstreams(t *testing.T) {
	// The inserted quality node should sit between meta and series; series'
	// upstream becomes the quality node, not meta directly.
	c := parseDAGOK(t, `
src    = input("rss", url="https://example.com/feed")
meta   = process("metainfo_file", upstream=src)
series = process("series", upstream=meta, static=["x"], quality="1080p")
output("print", upstream=series)
pipeline("p")
`)
	g := c.Graphs["p"]
	var qualityID, seriesID dag.NodeID
	for _, n := range g.Nodes() {
		switch n.PluginName {
		case "quality":
			qualityID = n.ID
		case "series":
			seriesID = n.ID
		}
	}
	if qualityID == "" || seriesID == "" {
		t.Fatal("missing expected nodes")
	}
	// series upstream must be the quality node.
	seriesUps := g.Node(seriesID).Upstreams
	if len(seriesUps) != 1 || seriesUps[0] != qualityID {
		t.Errorf("series upstream should be %q; got %v", qualityID, seriesUps)
	}
}

// Inside a # pipeliner-annotated function, the legacy quality= knob still
// triggers the migration. The injected node lands among the function's
// internal nodes; the web UI bubbles the marker up to the function-call card.
func TestMigrateInsideUserFunction(t *testing.T) {
	c := parseDAGOK(t, `
# pipeliner:param quality  Minimum quality spec
def filter_movies(upstream, quality):
    req = process("require", upstream=upstream, fields=["title", "video_year", "_quality"])
    return process("movies", upstream=req, quality=quality, static=["Inception"])

src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)
out  = filter_movies(meta, "1080p+")
output("print", upstream=out)
pipeline("p")
`)
	g := c.Graphs["p"]
	var quality *dag.Node
	for _, n := range g.Nodes() {
		if n.PluginName == "quality" {
			quality = n
			break
		}
	}
	if quality == nil {
		t.Fatal("expected an auto-injected quality node from migration inside the function body")
	}
	if quality.AutoMigrated == "" {
		t.Error("injected quality node should be tagged AutoMigrated")
	}

	// The injected node should be part of the function call's internal nodes
	// so the visual editor associates it with the function-call card.
	calls := c.FunctionCalls["p"]
	if len(calls) != 1 {
		t.Fatalf("expected 1 function call record; got %d", len(calls))
	}
	if !slices.Contains(calls[0].InternalNodeIDs, string(quality.ID)) {
		t.Errorf("injected quality node %q should be in the function call's internal node IDs %v",
			quality.ID, calls[0].InternalNodeIDs)
	}

	if len(c.LoadWarnings) == 0 {
		t.Error("expected a deprecation warning even when the deprecated key is hidden inside a function body")
	}
}

func TestNoMigrationWhenQualityAbsent(t *testing.T) {
	c := parseDAGOK(t, `
src    = input("rss", url="https://example.com/feed")
meta   = process("metainfo_file", upstream=src)
series = process("series", upstream=meta, static=["x"])
output("print", upstream=series)
pipeline("p")
`)
	g := c.Graphs["p"]
	if slices.Contains(pluginNamesIn(g), "quality") {
		t.Errorf("unexpected quality node inserted; nodes=%v", pluginNamesIn(g))
	}
	if len(c.LoadWarnings) != 0 {
		t.Errorf("no warnings expected; got %v", c.LoadWarnings)
	}
}
