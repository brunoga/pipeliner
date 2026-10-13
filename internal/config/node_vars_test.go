package config

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"

	_ "github.com/brunoga/pipeliner/plugins/processor/filter/route"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/sink/print"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
)

// varsByName inverts NodeVars so a test can ask "which node is `x`".
func varsByName(t *testing.T, src string) (map[string]string, *Config) {
	t.Helper()
	c, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	out := map[string]string{}
	for id, v := range c.NodeVars {
		out[v] = string(id)
	}
	return out, c
}

// NodeVars is read from the module globals, so it names each node exactly,
// whatever the loader's counter did. The visual editor writes nodes back
// under these names; before it existed, a save renamed every hand-named node
// to its ID.
func TestNodeVarsNamesEachNodeByItsVariable(t *testing.T) {
	byName, c := varsByName(t, `
feed      = input("rss", url="https://example.com/a")
fresh     = process("seen", upstream=feed)
shown     = output("print", upstream=fresh)
pipeline("tv")
`)
	for _, v := range []string{"feed", "fresh", "shown"} {
		id, ok := byName[v]
		if !ok {
			t.Fatalf("variable %q has no node (NodeVars %v)", v, c.NodeVars)
		}
		if c.Graphs["tv"].Node(dagID(id)) == nil {
			t.Errorf("variable %q maps to %q, which is not a node", v, id)
		}
	}
}

// The case order-based pairing got wrong: an unassigned call creates a node
// with no variable, so pairing "the Nth print node with the Nth print
// assignment" handed the unassigned node the next one's name.
func TestNodeVarsSkipsUnassignedCalls(t *testing.T) {
	byName, c := varsByName(t, `
feed  = input("rss", url="https://example.com/a")
output("print", upstream=feed)
named = output("print", upstream=feed)
pipeline("tv")
`)
	id := byName["named"]
	n := c.Graphs["tv"].Node(dagID(id))
	if n == nil {
		t.Fatalf("named → %q, not a node", id)
	}
	// The unassigned print is created first, so it has the lower counter.
	var unassigned string
	for _, m := range c.Graphs["tv"].Nodes() {
		if m.PluginName == "print" && string(m.ID) != id {
			unassigned = string(m.ID)
		}
	}
	if _, ok := c.NodeVars[dagID(unassigned)]; ok {
		t.Errorf("the unassigned print node %q was given a name: %q", unassigned, c.NodeVars[dagID(unassigned)])
	}
}

// route() returns port handles, not a node, but the variable holding them
// names the route node — `lanes = route(...)` is what the editor must write.
func TestNodeVarsNamesRouteNodes(t *testing.T) {
	byName, c := varsByName(t, `
feed  = input("rss", url="https://example.com/a")
lanes = route(feed, recent="video_year >= 2020", rest="true")
a = output("print", upstream=lanes.recent)
b = output("print", upstream=lanes.rest)
pipeline("tv")
`)
	id, ok := byName["lanes"]
	if !ok {
		t.Fatalf("route variable has no node (NodeVars %v)", c.NodeVars)
	}
	if n := c.Graphs["tv"].Node(dagID(id)); n == nil || n.PluginName != "route" {
		t.Errorf("lanes → %q, want the route node", id)
	}
}

// Nodes inside a user function are locals, and the variable a call is
// assigned to is the call's key — neither is a plugin node's name.
func TestNodeVarsExcludesFunctionNodes(t *testing.T) {
	_, c := varsByName(t, `
# pipeliner:param x
def helper(upstream, x):
    inner = process("seen", upstream=upstream)
    return inner

feed = input("rss", url="https://example.com/a")
out  = helper(upstream=feed, x="1")
end  = output("print", upstream=out)
pipeline("tv")
`)
	for id, v := range c.NodeVars {
		if v == "inner" || v == "out" {
			t.Errorf("node %q was named %q, which is a function local or a call key", id, v)
		}
	}
}

func dagID(s string) dag.NodeID { return dag.NodeID(s) }
