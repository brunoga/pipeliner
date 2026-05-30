package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"

	// Register plugins so the field computation has real descriptors.
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/condition"
	_ "github.com/brunoga/pipeliner/plugins/sink/print"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newFieldsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fields", srv.apiFields)
	return httptest.NewServer(mux)
}

func newConditionParseServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/config/parse", srv.apiConfigParse)
	return httptest.NewServer(mux)
}

// parseNodes extracts the node list from a graph in the parse response.
func parseNodes(t *testing.T, result map[string]any, graphName string) []any {
	t.Helper()
	graphs, ok := result["graphs"].(map[string]any)
	if !ok {
		t.Fatalf("graphs missing in response")
	}
	g, ok := graphs[graphName].(map[string]any)
	if !ok {
		t.Fatalf("graph %q missing; graphs = %v", graphName, graphs)
	}
	nodes, _ := g["nodes"].([]any)
	return nodes
}

// nodeByPlugin finds the first node with the given plugin name.
func nodeByPlugin(nodes []any, plugin string) map[string]any {
	for _, n := range nodes {
		nm, _ := n.(map[string]any)
		if nm["plugin"] == plugin {
			return nm
		}
	}
	return nil
}

// fieldSets extracts the "fields" object from a node response.
func fieldSets(t *testing.T, node map[string]any) (certain, reachable []string) {
	t.Helper()
	fields, ok := node["fields"].(map[string]any)
	if !ok {
		t.Fatalf("fields missing on node %v", node["plugin"])
	}
	toStrSlice := func(v any) []string {
		arr, _ := v.([]any)
		out := make([]string, 0, len(arr))
		for _, a := range arr {
			if s, ok := a.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return toStrSlice(fields["certain"]), toStrSlice(fields["reachable"])
}

func containsField(fields []string, name string) bool {
	for _, f := range fields {
		if f == name {
			return true
		}
	}
	return false
}

// getFields is a test helper that fetches /api/fields with a context so the
// noctx linter is satisfied.
func getFields(t *testing.T, baseURL string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/api/fields", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/fields: %v", err)
	}
	return resp
}

// ── GET /api/fields ───────────────────────────────────────────────────────────

func TestAPIFieldsReturnsArray(t *testing.T) {
	ts := newFieldsServer(t)
	defer ts.Close()

	resp := getFields(t, ts.URL)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var fields []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(fields) == 0 {
		t.Error("expected non-empty fields array")
	}
}

func TestAPIFieldsContainsRequiredProperties(t *testing.T) {
	ts := newFieldsServer(t)
	defer ts.Close()

	resp := getFields(t, ts.URL)
	defer resp.Body.Close()

	var fields []map[string]any
	json.NewDecoder(resp.Body).Decode(&fields) //nolint:errcheck

	for _, f := range fields {
		name, _ := f["name"].(string)
		if name == "" {
			t.Errorf("field missing name: %v", f)
			continue
		}
		typ, _ := f["type"].(string)
		if typ == "" {
			t.Errorf("field %q missing type", name)
		}
		desc, _ := f["description"].(string)
		if desc == "" {
			t.Errorf("field %q missing description", name)
		}
	}
}

func TestAPIFieldsContainsWellKnownFields(t *testing.T) {
	ts := newFieldsServer(t)
	defer ts.Close()

	resp := getFields(t, ts.URL)
	defer resp.Body.Close()

	var fields []map[string]any
	json.NewDecoder(resp.Body).Decode(&fields) //nolint:errcheck

	wanted := map[string]string{
		"source":  "string",
		"title":   "string",
		"enriched": "bool",
		"torrent_seeds": "int",
		"video_year":    "int",
		"series_season": "int",
	}
	found := make(map[string]string, len(wanted))
	for _, f := range fields {
		name, _ := f["name"].(string)
		typ, _  := f["type"].(string)
		if _, ok := wanted[name]; ok {
			found[name] = typ
		}
	}
	for name, wantType := range wanted {
		if got, ok := found[name]; !ok {
			t.Errorf("field %q not found in /api/fields response", name)
		} else if got != wantType {
			t.Errorf("field %q type: got %q, want %q", name, got, wantType)
		}
	}
}

func TestAPIFieldsKnownValuesForEnum(t *testing.T) {
	ts := newFieldsServer(t)
	defer ts.Close()

	resp := getFields(t, ts.URL)
	defer resp.Body.Close()

	var fields []map[string]any
	json.NewDecoder(resp.Body).Decode(&fields) //nolint:errcheck

	for _, f := range fields {
		if f["name"] == "torrent_link_type" {
			kv, _ := f["known_values"].([]any)
			if len(kv) == 0 {
				t.Error("torrent_link_type should have known_values")
			}
			vals := make([]string, 0, len(kv))
			for _, v := range kv {
				if s, ok := v.(string); ok {
					vals = append(vals, s)
				}
			}
			hasT := containsField(vals, "torrent")
			hasM := containsField(vals, "magnet")
			if !hasT || !hasM {
				t.Errorf("torrent_link_type known_values: got %v, want [torrent magnet]", vals)
			}
			return
		}
	}
	t.Error("torrent_link_type not found in /api/fields")
}

func TestAPIFieldsExposesDeprecation(t *testing.T) {
	// Temporarily inject a deprecated field so we can confirm the JSON shape
	// surfaces the deprecation attributes end-to-end.
	orig := entry.KnownFields
	entry.KnownFields = append(append([]entry.FieldMeta{}, orig...), entry.FieldMeta{
		Name:        "_test_deprecated_field",
		Type:        entry.FieldTypeString,
		Description: "a test-only deprecated field",
		Deprecated:  true,
		ReplacedBy:  "title",
	})
	t.Cleanup(func() { entry.KnownFields = orig })

	ts := newFieldsServer(t)
	defer ts.Close()

	resp := getFields(t, ts.URL)
	defer resp.Body.Close()

	var fields []map[string]any
	json.NewDecoder(resp.Body).Decode(&fields) //nolint:errcheck

	for _, f := range fields {
		if f["name"] != "_test_deprecated_field" {
			continue
		}
		if dep, _ := f["deprecated"].(bool); !dep {
			t.Errorf("deprecated: want true, got %v", f["deprecated"])
		}
		if rb, _ := f["replaced_by"].(string); rb != "title" {
			t.Errorf("replaced_by: want %q, got %v", "title", f["replaced_by"])
		}
		return
	}
	t.Error("_test_deprecated_field not found in /api/fields response")
}

// ── POST /api/config/parse — fields in response ───────────────────────────────

func TestAPIConfigParseSourceNodeHasEmptyInputFields(t *testing.T) {
	ts := newConditionParseServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{
		"content": `
src = input("rss", url="https://example.com/rss")
output("print", upstream=src)
pipeline("t")
`,
	})
	resp := post(t, ts.URL+"/api/config/parse", "application/json", body)
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck

	nodes := parseNodes(t, result, "t")
	rssNode := nodeByPlugin(nodes, "rss")
	if rssNode == nil {
		t.Fatal("rss node not found")
	}

	certain, reachable := fieldSets(t, rssNode)
	// Source node: nothing enters it, so input fields are empty.
	if len(certain) != 0 {
		t.Errorf("rss source node should have empty certain fields, got %v", certain)
	}
	if len(reachable) != 0 {
		t.Errorf("rss source node should have empty reachable fields, got %v", reachable)
	}
}

func TestAPIConfigParseDownstreamNodeSeesUpstreamFields(t *testing.T) {
	ts := newConditionParseServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{
		"content": `
src  = input("rss", url="https://example.com/rss")
cond = process("condition", upstream=src)
output("print", upstream=cond)
pipeline("t")
`,
	})
	resp := post(t, ts.URL+"/api/config/parse", "application/json", body)
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck

	nodes := parseNodes(t, result, "t")

	// condition node input = rss output
	condNode := nodeByPlugin(nodes, "condition")
	if condNode == nil {
		t.Fatal("condition node not found")
	}
	certain, reachable := fieldSets(t, condNode)

	// rss Produces: source, title, rss_feed — must be certain
	for _, f := range []string{"source", "title", "rss_feed"} {
		if !containsField(certain, f) {
			t.Errorf("condition node certain fields: want %q, got %v", f, certain)
		}
	}
	// rss MayProduce: description — must be reachable
	if !containsField(reachable, "description") {
		t.Errorf("condition node reachable: want description, got %v", reachable)
	}
	// Every certain field must also be reachable (certain ⊆ reachable).
	for _, f := range certain {
		if !containsField(reachable, f) {
			t.Errorf("field %q is certain but not reachable (certain must be a subset)", f)
		}
	}
}

func TestAPIConfigParseFieldSetsAreNeverNull(t *testing.T) {
	// fields.certain and fields.reachable must be JSON arrays, never null.
	ts := newConditionParseServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{
		"content": `
src = input("rss", url="https://example.com/rss")
output("print", upstream=src)
pipeline("t")
`,
	})
	resp := post(t, ts.URL+"/api/config/parse", "application/json", body)
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	raw, _ := json.Marshal(result)

	// The raw JSON must not contain "certain":null or "reachable":null
	s := string(raw)
	if idx := len(s); idx == 0 {
		t.Fatal("empty response")
	}
	for _, bad := range []string{`"certain":null`, `"reachable":null`} {
		if contains := func(h, n string) bool {
			for i := range h {
				if i+len(n) <= len(h) && h[i:i+len(n)] == n {
					return true
				}
			}
			return false
		}; contains(s, bad) {
			t.Errorf("response contains %q — must be empty array, not null", bad)
		}
	}
}
