package dag_test

import (
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func collapsingDesc(name string) *plugin.Descriptor {
	return &plugin.Descriptor{
		PluginName: name, Role: plugin.RoleProcessor,
		Refusal: plugin.RefusalPerItem, Collapses: true,
	}
}

func refusingDesc(name string, r plugin.Refusal) *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: name, Role: plugin.RoleProcessor, Refusal: r}
}

func warnText(warnings []error) string {
	var b strings.Builder
	for _, w := range warnings {
		b.WriteString(w.Error())
		b.WriteString("\n")
	}
	return b.String()
}

// The Airplane II case: dedup collapses a wave to one release, and content
// then rejects that release as a RAR archive — by which point the dozen
// alternatives, one of them explicitly tagged NORAR, are already gone.
func TestWarnsWhenAPerReleaseRefusalFollowsACollapse(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("dd", "dedup", "src"),
		node("content", "content", "dd"),
		node("out", "transmission", "content"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		collapsingDesc("dedup"),
		refusingDesc("content", plugin.RefusalPerRelease),
		sinkDescFor("transmission"),
	)
	errs, warnings := dag.Validate(g, reg)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	got := warnText(warnings)
	if !strings.Contains(got, `node "content"`) || !strings.Contains(got, `node "dd"`) {
		t.Errorf("warning should name both nodes, got:\n%s", got)
	}
	if !strings.Contains(got, "move \"content\" above \"dd\"") {
		t.Errorf("warning should say what to do, got:\n%s", got)
	}
}

func TestNoWarningWhenTheRefusalRunsFirst(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("content", "content", "src"),
		node("dd", "dedup", "content"),
		node("out", "transmission", "dd"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		refusingDesc("content", plugin.RefusalPerRelease),
		collapsingDesc("dedup"),
		sinkDescFor("transmission"),
	)
	if _, warnings := dag.Validate(g, reg); len(warnings) > 0 {
		t.Errorf("correct ordering must not warn, got:\n%s", warnText(warnings))
	}
}

// A per-item refusal after the collapse is fine: it would refuse every other
// release of the same item for the same reason, so nothing is lost.
func TestPerItemRefusalAfterACollapseIsFine(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("dd", "dedup", "src"),
		node("seen", "seen", "dd"),
		node("out", "transmission", "seen"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		collapsingDesc("dedup"),
		refusingDesc("seen", plugin.RefusalPerItem),
		sinkDescFor("transmission"),
	)
	if _, warnings := dag.Validate(g, reg); len(warnings) > 0 {
		t.Errorf("per-item refusal must not warn, got:\n%s", warnText(warnings))
	}
}

func TestRefusalNoneAfterACollapseIsFine(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("dd", "dedup", "src"),
		node("fmt", "pathfmt", "dd"),
		node("out", "transmission", "fmt"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		collapsingDesc("dedup"),
		refusingDesc("pathfmt", plugin.RefusalNone),
		sinkDescFor("transmission"),
	)
	if _, warnings := dag.Validate(g, reg); len(warnings) > 0 {
		t.Errorf("non-refusing node must not warn, got:\n%s", warnText(warnings))
	}
}

// The refusal need not be adjacent — it is reached through enrichment nodes.
func TestWarnsThroughIntermediateNodes(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("dd", "dedup", "src"),
		node("meta", "metainfo_tmdb", "dd"),
		node("fmt", "pathfmt", "meta"),
		node("rate", "bitrate", "fmt"),
		node("out", "transmission", "rate"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		collapsingDesc("dedup"),
		refusingDesc("metainfo_tmdb", plugin.RefusalNone),
		refusingDesc("pathfmt", plugin.RefusalNone),
		refusingDesc("bitrate", plugin.RefusalPerRelease),
		sinkDescFor("transmission"),
	)
	_, warnings := dag.Validate(g, reg)
	if !strings.Contains(warnText(warnings), `node "rate"`) {
		t.Errorf("should warn about a refusal two hops down, got:\n%s", warnText(warnings))
	}
}

// A refusal on an optional fan-out branch costs that branch, not the item:
// the sibling branch still reaches a sink. This is the shape of a pipeline
// that downloads an episode on one branch and adds the show to a favourites
// list on the other — the favourites branch may refuse freely.
func TestNoWarningWhenASiblingBranchStillReachesASink(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("dd", "dedup", "src"),
		node("download", "pathfmt", "dd"),
		node("optional", "content", "dd"),
		node("outa", "transmission", "download"),
		node("outb", "list_add", "optional"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		collapsingDesc("dedup"),
		refusingDesc("pathfmt", plugin.RefusalNone),
		refusingDesc("content", plugin.RefusalPerRelease),
		sinkDescFor("transmission"),
		sinkDescFor("list_add"),
	)
	if _, warnings := dag.Validate(g, reg); len(warnings) > 0 {
		t.Errorf("an optional branch must not warn, got:\n%s", warnText(warnings))
	}
}

// But when every branch out of the collapse passes through the refusal, the
// item reaches no sink at all — that is the case worth warning about.
func TestWarnsWhenEveryBranchPassesThroughTheRefusal(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("dd", "dedup", "src"),
		node("gate", "content", "dd"),
		node("a", "pathfmt", "gate"),
		node("b", "pathfmt", "gate"),
		node("outa", "transmission", "a"),
		node("outb", "list_add", "b"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		collapsingDesc("dedup"),
		refusingDesc("content", plugin.RefusalPerRelease),
		refusingDesc("pathfmt", plugin.RefusalNone),
		sinkDescFor("transmission"),
		sinkDescFor("list_add"),
	)
	_, warnings := dag.Validate(g, reg)
	if !strings.Contains(warnText(warnings), `node "gate"`) {
		t.Errorf("a refusal every branch passes through must warn, got:\n%s", warnText(warnings))
	}
}

// Every offending node is named, not just the first.
func TestReportsEveryOffendingNode(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("dd", "dedup", "src"),
		node("req", "require", "dd"),
		node("content", "content", "req"),
		node("out", "transmission", "content"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		collapsingDesc("dedup"),
		refusingDesc("require", plugin.RefusalPerRelease),
		refusingDesc("content", plugin.RefusalPerRelease),
		sinkDescFor("transmission"),
	)
	_, warnings := dag.Validate(g, reg)
	got := warnText(warnings)
	if !strings.Contains(got, `node "req"`) || !strings.Contains(got, `node "content"`) {
		t.Errorf("both offenders should be named, got:\n%s", got)
	}
}

// No collapsing node means nothing to order against.
func TestNoCollapseNoWarning(t *testing.T) {
	g := makeGraph(t,
		node("src", "rss"),
		node("content", "content", "src"),
		node("out", "transmission", "content"),
	)
	reg := makeRegistry(
		sourceDescFor("rss"),
		refusingDesc("content", plugin.RefusalPerRelease),
		sinkDescFor("transmission"),
	)
	if _, warnings := dag.Validate(g, reg); len(warnings) > 0 {
		t.Errorf("no collapse, no warning; got:\n%s", warnText(warnings))
	}
}
