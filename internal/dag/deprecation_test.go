package dag_test

import (
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// injectDeprecatedField appends a Deprecated FieldMeta to entry.KnownFields for
// the duration of the test, then restores the original slice. Tests that mutate
// KnownFields must not run in parallel.
func injectDeprecatedField(t *testing.T, name, replacedBy string) {
	t.Helper()
	orig := entry.KnownFields
	entry.KnownFields = append(append([]entry.FieldMeta{}, orig...), entry.FieldMeta{
		Name:       name,
		Type:       entry.FieldTypeString,
		Deprecated: true,
		ReplacedBy: replacedBy,
	})
	t.Cleanup(func() { entry.KnownFields = orig })
}

// containsAll checks that every needle appears as a substring of at least one
// element of haystack. Used to assert warnings contain the expected phrases
// without coupling to exact formatting.
func containsAll(haystack []error, needles ...string) bool {
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if strings.Contains(h.Error(), n) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestDeprecation_WarnsOnRequires(t *testing.T) {
	injectDeprecatedField(t, "movie_title_test", "title")

	src := sourceDescFor("src")
	src.Produces = []string{"movie_title_test"}
	proc := processorDescFor("proc")
	proc.Requires = [][]string{{"movie_title_test"}}

	g := makeGraph(t,
		node("a", "src"),
		node("b", "proc", "a"),
	)
	reg := makeRegistry(src, proc)

	_, warnings := dag.Validate(g, reg)
	if !containsAll(warnings, "deprecated", "movie_title_test", `"title"`) {
		t.Fatalf("expected deprecation warning, got: %v", warnings)
	}
}

func TestDeprecation_WarnsOnConditionRule(t *testing.T) {
	injectDeprecatedField(t, "movie_title_test", "title")

	src := sourceDescFor("src")
	src.Produces = []string{"movie_title_test"}
	cond := processorDescFor("condition")

	condNode := node("b", "condition", "a")
	condNode.Config = map[string]any{
		"reject": `movie_title_test == ""`,
	}

	g := makeGraph(t, node("a", "src"), condNode)
	reg := makeRegistry(src, cond)

	_, warnings := dag.Validate(g, reg)
	if !containsAll(warnings, "deprecated", "movie_title_test", "condition rule") {
		t.Fatalf("expected condition-rule deprecation warning, got: %v", warnings)
	}
}

func TestDeprecation_WarnsOnRouteExpression(t *testing.T) {
	injectDeprecatedField(t, "movie_title_test", "title")

	src := sourceDescFor("src")
	src.Produces = []string{"movie_title_test"}
	rsel := processorDescFor("route_selector")
	sink := sinkDescFor("sink")

	routeNode := node("b", "route_selector", "a")
	routeNode.Config = map[string]any{
		"_port_accept_expr": `movie_title_test != ""`,
		"_route_group":      "g1",
	}

	g := makeGraph(t,
		node("a", "src"),
		routeNode,
		node("c", "sink", "b"),
	)
	reg := makeRegistry(src, rsel, sink)

	_, warnings := dag.Validate(g, reg)
	if !containsAll(warnings, "deprecated", "movie_title_test", "route port expression") {
		t.Fatalf("expected route-port deprecation warning, got: %v", warnings)
	}
}

func TestDeprecation_WarnsOnRequireFields(t *testing.T) {
	injectDeprecatedField(t, "movie_title_test", "title")

	src := sourceDescFor("src")
	src.Produces = []string{"movie_title_test"}
	req := processorDescFor("require")

	requireNode := node("b", "require", "a")
	requireNode.Config = map[string]any{
		"fields": []any{"movie_title_test"},
	}

	g := makeGraph(t, node("a", "src"), requireNode)
	reg := makeRegistry(src, req)

	_, warnings := dag.Validate(g, reg)
	if !containsAll(warnings, "deprecated", "movie_title_test", "require fields") {
		t.Fatalf("expected require-fields deprecation warning, got: %v", warnings)
	}
}

func TestDeprecation_WarnsOnPatternSchema(t *testing.T) {
	injectDeprecatedField(t, "movie_title_test", "title")

	src := sourceDescFor("src")
	src.Produces = []string{"movie_title_test"}
	pfmt := processorDescFor("pathfmt")
	pfmt.Schema = []plugin.FieldSchema{
		{Key: "path", Type: plugin.FieldTypePattern},
	}

	pathNode := node("b", "pathfmt", "a")
	pathNode.Config = map[string]any{
		"path": "/data/{movie_title_test}",
	}

	g := makeGraph(t, node("a", "src"), pathNode)
	reg := makeRegistry(src, pfmt)

	_, warnings := dag.Validate(g, reg)
	if !containsAll(warnings, "deprecated", "movie_title_test", `pattern "path"`) {
		t.Fatalf("expected pattern deprecation warning, got: %v", warnings)
	}
}

// TestDeprecation_MovieTitleIsDeprecated is a smoke test that the real
// movie_title field is registered as deprecated and surfaces a warning when
// referenced — no test-only injection. If this fails because the deprecation
// flag was removed, the test should be updated to mark a different field
// (or deleted along with the deprecation framework).
func TestDeprecation_MovieTitleIsDeprecated(t *testing.T) {
	src := sourceDescFor("src")
	src.Produces = []string{entry.FieldMovieTitle}
	req := processorDescFor("require")

	requireNode := node("b", "require", "a")
	requireNode.Config = map[string]any{
		"fields": []any{entry.FieldMovieTitle},
	}

	g := makeGraph(t, node("a", "src"), requireNode)
	reg := makeRegistry(src, req)

	_, warnings := dag.Validate(g, reg)
	if !containsAll(warnings, "deprecated", entry.FieldMovieTitle, "title") {
		t.Fatalf("expected real movie_title deprecation warning, got: %v", warnings)
	}
}

func TestDeprecation_NoWarningWhenFieldUndeprecated(t *testing.T) {
	// "title" is a known, non-deprecated field. References should not produce
	// deprecation warnings even when statically detectable.
	src := sourceDescFor("src")
	src.Produces = []string{entry.FieldTitle}
	proc := processorDescFor("proc")
	proc.Requires = [][]string{{entry.FieldTitle}}

	g := makeGraph(t,
		node("a", "src"),
		node("b", "proc", "a"),
	)
	reg := makeRegistry(src, proc)

	_, warnings := dag.Validate(g, reg)
	for _, w := range warnings {
		if strings.Contains(w.Error(), "deprecated") {
			t.Fatalf("unexpected deprecation warning for non-deprecated field: %v", w)
		}
	}
}
