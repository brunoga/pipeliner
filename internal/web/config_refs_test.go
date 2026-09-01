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
