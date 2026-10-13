package web

import (
	"github.com/brunoga/pipeliner/internal/config"
	"reflect"
	"testing"
)

func TestConfigRefs(t *testing.T) {
	src := `
KEY = env("K")
CONF = {"a": 1}
LIST = [{"x": KEY}]
n1 = input("rss", url="https://lit", api_key=KEY, limit=5)
n2 = output("notify", upstream=n1, config=CONF, body="a,b,{{x}}")
n3 = process("movies", upstream=n1, list=LIST, static=["A", "B"])
n4 = process("discover", upstream=n1, search=[{"name": "jackett", "api_key": KEY, "n": 10}])
pipeline("p")
`
	refs := configRefs(src)

	// n1: api_key is a reference; url/limit are literals (omitted).
	want1 := map[string]any{"api_key": map[string]any{"__star_raw__": "KEY"}}
	if !reflect.DeepEqual(refs["n1"], want1) {
		t.Errorf("n1 = %#v, want %#v", refs["n1"], want1)
	}

	// n2: config is a reference; the comma/brace-laden body literal must NOT be
	// captured (it has no identifier) and must not corrupt parsing.
	want2 := map[string]any{"config": map[string]any{"__star_raw__": "CONF"}}
	if !reflect.DeepEqual(refs["n2"], want2) {
		t.Errorf("n2 = %#v, want %#v", refs["n2"], want2)
	}

	// n3: list is a reference; the literal static list is omitted.
	want3 := map[string]any{"list": map[string]any{"__star_raw__": "LIST"}}
	if !reflect.DeepEqual(refs["n3"], want3) {
		t.Errorf("n3 = %#v, want %#v", refs["n3"], want3)
	}

	// n4: inline dict mixing literals and a reference — the reference is
	// preserved as a marker, the literal siblings keep their values.
	inner := refs["n4"]["search"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(inner["api_key"], map[string]any{"__star_raw__": "KEY"}) {
		t.Errorf("n4 search[0].api_key = %#v", inner["api_key"])
	}
	if inner["name"] != "jackett" {
		t.Errorf("n4 search[0].name = %#v, want jackett", inner["name"])
	}
	if inner["n"] != int64(10) {
		t.Errorf("n4 search[0].n = %#v, want 10", inner["n"])
	}
}

func TestConfigRefsSkipsUpstreamAndLiterals(t *testing.T) {
	src := `
X = env("X")
n = input("rss", url="lit", count=3, flag=True)
pipeline("p")
`
	refs := configRefs(src)
	if _, ok := refs["n"]; ok {
		t.Errorf("a node with only literal kwargs should have no refs, got %#v", refs["n"])
	}
}

func TestConfigRefsInvalidSourceIsSafe(t *testing.T) {
	if refs := configRefs("this is not valid starlark ((("); len(refs) != 0 {
		t.Errorf("invalid source should yield no refs, got %#v", refs)
	}
}

// idToVarFromLoader runs content through the real loader and returns the
// node ID → variable map the parse endpoint builds from it.
func idToVarFromLoader(t *testing.T, content string) map[string]string {
	t.Helper()
	c, err := config.ParseBytes([]byte(content))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	out := map[string]string{}
	for id, v := range c.NodeVars {
		if string(id) != v {
			out[string(id)] = v
		}
	}
	return out
}

// A run of same-plugin nodes after a deletion. The global ID counter shifts
// every later node down by one while the variable names keep their numbers,
// so each node's own ID is now the name of its NEIGHBOUR's variable. The
// lookup must follow the mapping, not the coincidental name.
//
// This is the live failure: node jackett_132 was assigned to variable
// jackett_133 (a BD100 search with a literal url), a variable jackett_132
// still existed (the BD66 search, url=JACKETT_URL), and one visual save gave
// the BD100 node the BD66 node's references.
func TestRekeyBySourceFollowsMappingWhenOwnIDNamesANeighbour(t *testing.T) {
	content := `
FEED = "https://example.com/shared"

# first: the shared feed
rss_1 = input("rss", url=FEED)

# second: a literal url of its own
rss_2 = input("rss", url="https://example.com/literal")

out_a = output("print", upstream=rss_1)
out_b = output("print", upstream=rss_2)
pipeline("tv")
`
	// rss_0 is not in the text, so the loader numbers the feeds from 0.
	idToVar := idToVarFromLoader(t, content)
	if idToVar["rss_1"] != "rss_2" {
		t.Fatalf("node rss_1 → %q, want variable rss_2 (map %v)", idToVar["rss_1"], idToVar)
	}

	refs := rekeyBySource(configRefs(content), idToVar)
	if got, ok := refs("rss_1"); ok {
		t.Errorf("node rss_1 (the literal-url node) picked up references %v — "+
			"those belong to variable rss_1, which is node rss_0", got)
	}
	if got, ok := refs("rss_0"); !ok || got["url"] == nil {
		t.Errorf("node rss_0 must keep its url=FEED reference, got %v", got)
	}

	comments, _, _, _ := scanComments(content)
	cmt := rekeyBySource(comments, idToVar)
	if got, _ := cmt("rss_1"); got != "second: a literal url of its own" {
		t.Errorf("node rss_1 comment = %q, want its own", got)
	}
	if got, _ := cmt("rss_0"); got != "first: the shared feed" {
		t.Errorf("node rss_0 comment = %q, want its own", got)
	}
}

// A comment block separated from the next statement by a blank line — a
// section header — used to be discarded at the blank line, so every visual
// save deleted it. It is now kept and joined to the next statement's comment.
func TestScanCommentsKeepsBlocksDetachedByABlankLine(t *testing.T) {
	content := `
# ── tv: a section header
# that runs to two lines

# attached to the feed
src = input("rss", url="https://example.com/a")

# a header with nothing attached below it

out = output("print", upstream=src)

# above the pipeline call, detached

# and attached
pipeline("tv")
`
	nodes, pipes, _, _ := scanComments(content)

	want := "── tv: a section header\nthat runs to two lines\n\nattached to the feed"
	if nodes["src"] != want {
		t.Errorf("src comment = %q\nwant          %q", nodes["src"], want)
	}
	if nodes["out"] != "a header with nothing attached below it" {
		t.Errorf("a detached block alone must still attach: out = %q", nodes["out"])
	}
	if pipes["tv"] != "above the pipeline call, detached\n\nand attached" {
		t.Errorf("pipeline comment = %q", pipes["tv"])
	}
}

// Detached comments must not leak across a statement that has nowhere to put
// them. A comment above a def belongs to the def (its source text keeps it),
// not to whatever node comes after the def's body.
func TestScanCommentsDoesNotCarryCommentsPastOtherStatements(t *testing.T) {
	content := `
# describes the helper

def helper(upstream):
    return process("seen", upstream=upstream)

src = input("rss", url="https://example.com/a")
`
	nodes, _, _, _ := scanComments(content)
	if got := nodes["src"]; got != "" {
		t.Errorf("src picked up a comment that belonged to the def: %q", got)
	}
}
