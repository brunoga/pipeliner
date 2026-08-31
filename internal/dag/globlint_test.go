package dag_test

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
)

// seriesNode builds a series node carrying the given static list, wired to a
// source, and returns the validation warnings. The series descriptor is a bare
// stub (no Requires/Produces) so only glob-lint warnings surface.
func staticListWarnings(t *testing.T, plugin string, static ...string) []error {
	t.Helper()
	items := make([]any, len(static))
	for i, s := range static {
		items[i] = s
	}
	b := &dag.Node{
		ID:         "b",
		PluginName: plugin,
		Upstreams:  []dag.NodeID{"a"},
		Config:     map[string]any{"static": items},
	}
	g := makeGraph(t, node("a", "src"), b)
	reg := makeRegistry(sourceDescFor("src"), processorDescFor(plugin))
	_, warnings := dag.Validate(g, reg)
	return warnings
}

func TestGlobLint_QuestionMarkFootgun(t *testing.T) {
	w := staticListWarnings(t, "series", "Who Framed Roger Rabbit?")
	if !containsAll(w, "glob metacharacter", "?", "Who Framed Roger Rabbit?") {
		t.Errorf("expected ? footgun warning, got %v", w)
	}
}

func TestGlobLint_MalformedPattern(t *testing.T) {
	w := staticListWarnings(t, "movies", "Nobody [")
	if !containsAll(w, "not a valid glob pattern", "Nobody [") {
		t.Errorf("expected malformed-pattern warning, got %v", w)
	}
}

func TestGlobLint_BracketClassWarns(t *testing.T) {
	w := staticListWarnings(t, "series", "Episode [abc]")
	if !containsAll(w, "glob metacharacter", "[") {
		t.Errorf("expected bracket warning, got %v", w)
	}
}

func TestGlobLint_TrailingStarNotFlagged(t *testing.T) {
	w := staticListWarnings(t, "series", "Star Wars*")
	if containsAll(w, "glob metacharacter") {
		t.Errorf("intentional trailing * should not warn, got %v", w)
	}
}

func TestGlobLint_CleanTitleNoWarning(t *testing.T) {
	w := staticListWarnings(t, "series", "Silo", "Breaking Bad", "9-1-1")
	if containsAll(w, "glob metacharacter") || containsAll(w, "not a valid glob pattern") {
		t.Errorf("clean titles should not warn, got %v", w)
	}
}

func TestGlobLint_EscapedMetacharNotFlagged(t *testing.T) {
	// A backslash-escaped '?' is a literal '?' to filepath.Match — intentional.
	w := staticListWarnings(t, "series", `Rabbit\?`)
	if containsAll(w, "glob metacharacter") {
		t.Errorf("escaped metacharacter should not warn, got %v", w)
	}
}

func TestGlobLint_OnlySeriesAndMovies(t *testing.T) {
	// A plugin not in the glob-pattern table is never scanned, even with a
	// static list that looks like a pattern.
	w := staticListWarnings(t, "someotherplugin", "Who?")
	if containsAll(w, "glob metacharacter") {
		t.Errorf("non-matching plugin should not be scanned, got %v", w)
	}
}
