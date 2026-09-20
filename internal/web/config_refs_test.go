package web

import (
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

// TestRemapBySourceOrderRecoversRenumberedNodes is the regression for the
// silent secret re-inlining: the loader numbers nodes with its own counter,
// so after a hand edit shifts the numbering, a node's ID no longer matches
// the source variable it was assigned to. Everything scanned from raw text
// is keyed by that variable name, so without reconciliation every reference
// (and comment, label, position) detaches and the visual editor rewrites
// resolved secrets into the config on the next save.
func TestRemapBySourceOrderRecoversRenumberedNodes(t *testing.T) {
	// Source names (rss_0, deluge_7) vs loader IDs (rss_2, deluge_9): the
	// numbering diverged by two when nodes were added earlier in the file.
	content := `
KEY = env("K")
rss_0 = input("rss", url="http://x/feed")
seen_1 = process("seen", upstream=rss_0)
deluge_7 = output("deluge", upstream=seen_1, password=KEY, host="h")
`
	ids := []string{"rss_2", "seen_3", "deluge_9"}
	plugins := map[string]string{"rss_2": "rss", "seen_3": "seen", "deluge_9": "deluge"}
	idToVar := remapBySourceOrder(content, ids, func(id string) string { return plugins[id] })

	if idToVar["deluge_9"] != "deluge_7" {
		t.Errorf("deluge_9 → %q, want deluge_7", idToVar["deluge_9"])
	}
	if idToVar["rss_2"] != "rss_0" {
		t.Errorf("rss_2 → %q, want rss_0", idToVar["rss_2"])
	}

	// The reference lookup must now find the node's refs by either key.
	refs := configRefs(content)
	lookup := rekeyBySource(refs, idToVar)
	got, ok := lookup("deluge_9")
	if !ok {
		t.Fatal("deluge_9 must resolve its source refs after remapping")
	}
	raw, isRaw := got["password"].(map[string]any)
	if !isRaw || raw["__star_raw__"] != "KEY" {
		t.Errorf("password ref = %#v, want {__star_raw__: KEY}", got["password"])
	}
}

// Identity case: a freshly-saved config whose source names already match the
// loader's IDs needs no remapping, and lookups still work.
func TestRemapBySourceOrderIdentity(t *testing.T) {
	content := `
KEY = env("K")
rss_0 = input("rss", url="http://x/feed")
deluge_1 = output("deluge", upstream=rss_0, password=KEY)
`
	ids := []string{"rss_0", "deluge_1"}
	plugins := map[string]string{"rss_0": "rss", "deluge_1": "deluge"}
	idToVar := remapBySourceOrder(content, ids, func(id string) string { return plugins[id] })
	if len(idToVar) != 0 {
		t.Errorf("matching names need no remap entries, got %v", idToVar)
	}
	lookup := rekeyBySource(configRefs(content), idToVar)
	if _, ok := lookup("deluge_1"); !ok {
		t.Error("identity lookup must still resolve refs")
	}
}

// User function calls create nodes internal to the call; those are excluded
// from the pairing by the caller, and their top-level assignment (a function
// call, not a plugin constructor) must not consume a plugin slot.
func TestSourceAssignmentsSkipsFunctionCalls(t *testing.T) {
	content := `
def helper(upstream):
    inner_0 = output("deluge", upstream=upstream, password="p")
    return inner_0

rss_0 = input("rss", url="http://x/feed")
helper_1 = helper(upstream=rss_0)
deluge_9 = output("deluge", upstream=rss_0, password="q")
`
	got := sourceAssignments(content)
	var pairs []string
	for _, a := range got {
		pairs = append(pairs, a.Plugin+":"+a.Var)
	}
	want := []string{"rss:rss_0", "deluge:deluge_9"}
	if len(pairs) != len(want) {
		t.Fatalf("assignments = %v, want %v", pairs, want)
	}
	for i := range want {
		if pairs[i] != want[i] {
			t.Errorf("assignments = %v, want %v", pairs, want)
			break
		}
	}
}

func TestNodeCreationOrder(t *testing.T) {
	got := nodeCreationOrder([]string{"deluge_10", "rss_2", "seen_9"})
	want := []string{"rss_2", "seen_9", "deluge_10"} // numeric, not lexical
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
