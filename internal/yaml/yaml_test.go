package yaml

import (
	"reflect"
	"testing"
)

// --- scalar parsing ---

func TestParseScalarNull(t *testing.T) {
	for _, s := range []string{"", "null", "~"} {
		if v := parseScalar(s); v != nil {
			t.Errorf("parseScalar(%q) = %v, want nil", s, v)
		}
	}
}

func TestParseScalarBool(t *testing.T) {
	cases := map[string]bool{"true": true, "True": true, "TRUE": true,
		"false": false, "False": false, "FALSE": false}
	for s, want := range cases {
		if v := parseScalar(s); v != want {
			t.Errorf("parseScalar(%q) = %v, want %v", s, v, want)
		}
	}
}

func TestParseScalarInt(t *testing.T) {
	if v := parseScalar("42"); v != int64(42) {
		t.Errorf("want int64(42), got %v (%T)", v, v)
	}
	if v := parseScalar("-7"); v != int64(-7) {
		t.Errorf("want int64(-7), got %v (%T)", v, v)
	}
}

func TestParseScalarFloat(t *testing.T) {
	if v := parseScalar("3.14"); v != float64(3.14) {
		t.Errorf("want float64(3.14), got %v (%T)", v, v)
	}
}

func TestParseScalarDoubleQuoted(t *testing.T) {
	if v := parseScalar(`"hello world"`); v != "hello world" {
		t.Errorf("want \"hello world\", got %v", v)
	}
	if v := parseScalar(`"has \"quotes\" inside"`); v != `has "quotes" inside` {
		t.Errorf("unexpected: %v", v)
	}
}

func TestParseScalarSingleQuoted(t *testing.T) {
	if v := parseScalar(`'hello world'`); v != "hello world" {
		t.Errorf("want \"hello world\", got %v", v)
	}
	// Single-quoted: no escape interpretation, inner " is literal
	if v := parseScalar(`'has "quotes" inside'`); v != `has "quotes" inside` {
		t.Errorf("unexpected: %v", v)
	}
}

func TestParseScalarPlainString(t *testing.T) {
	if v := parseScalar("http://example.com"); v != "http://example.com" {
		t.Errorf("unexpected: %v", v)
	}
	if v := parseScalar("1h"); v != "1h" {
		t.Errorf("unexpected: %v", v)
	}
}

// --- splitKey ---

func TestSplitKeySimple(t *testing.T) {
	key, val, ok := splitKey("name: value")
	if !ok || key != "name" || val != "value" {
		t.Errorf("got (%q, %q, %v)", key, val, ok)
	}
}

func TestSplitKeyEmptyVal(t *testing.T) {
	key, val, ok := splitKey("name:")
	if !ok || key != "name" || val != "" {
		t.Errorf("got (%q, %q, %v)", key, val, ok)
	}
}

func TestSplitKeyURL(t *testing.T) {
	key, val, ok := splitKey("url: http://example.com:8080/rss")
	if !ok || key != "url" || val != "http://example.com:8080/rss" {
		t.Errorf("got (%q, %q, %v)", key, val, ok)
	}
}

func TestSplitKeyNotMapping(t *testing.T) {
	for _, s := range []string{"- item", "-", "plain string", "no colon here"} {
		_, _, ok := splitKey(s)
		if ok {
			t.Errorf("splitKey(%q) should return false", s)
		}
	}
}

func TestSplitKeyQuotedKey(t *testing.T) {
	key, val, ok := splitKey(`"my-key": value`)
	if !ok || key != "my-key" || val != "value" {
		t.Errorf("got (%q, %q, %v)", key, val, ok)
	}
}

// --- document parsing ---

func TestUnmarshalSimpleMapping(t *testing.T) {
	var m map[string]string
	err := Unmarshal([]byte("key: value\nother: hello"), &m)
	if err != nil {
		t.Fatal(err)
	}
	if m["key"] != "value" || m["other"] != "hello" {
		t.Errorf("got %v", m)
	}
}

func TestUnmarshalNestedMapping(t *testing.T) {
	yaml := `
outer:
  inner: value
  num: 42
`
	var m map[string]map[string]any
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	if m["outer"]["inner"] != "value" {
		t.Errorf("inner: got %v", m["outer"]["inner"])
	}
	// JSON round-trip: int64 → json number → float64
	if m["outer"]["num"] != float64(42) {
		t.Errorf("num: got %v (%T)", m["outer"]["num"], m["outer"]["num"])
	}
}

func TestUnmarshalSequence(t *testing.T) {
	yaml := `
items:
  - alpha
  - beta
  - gamma
`
	var m map[string][]string
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(m["items"], want) {
		t.Errorf("got %v, want %v", m["items"], want)
	}
}

func TestUnmarshalQuotedStrings(t *testing.T) {
	yaml := `
a: "double quoted"
b: 'single quoted'
c: "has \"escapes\""
`
	var m map[string]string
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	if m["a"] != "double quoted" {
		t.Errorf("a: got %q", m["a"])
	}
	if m["b"] != "single quoted" {
		t.Errorf("b: got %q", m["b"])
	}
	if m["c"] != `has "escapes"` {
		t.Errorf("c: got %q", m["c"])
	}
}

func TestUnmarshalComments(t *testing.T) {
	yaml := `
# top-level comment
key: value # inline comment
other: hello
`
	var m map[string]string
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	if m["key"] != "value" {
		t.Errorf("key: got %q", m["key"])
	}
}

func TestUnmarshalNullValue(t *testing.T) {
	yaml := `
present: hello
empty:
nullval: null
tilde: ~
`
	var m map[string]any
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	if m["present"] != "hello" {
		t.Errorf("present: got %v", m["present"])
	}
	if m["empty"] != nil {
		t.Errorf("empty: want nil, got %v", m["empty"])
	}
	if m["nullval"] != nil {
		t.Errorf("nullval: want nil, got %v", m["nullval"])
	}
	if m["tilde"] != nil {
		t.Errorf("tilde: want nil, got %v", m["tilde"])
	}
}

func TestUnmarshalBooleans(t *testing.T) {
	yaml := `
yes_val: true
no_val: false
`
	var m map[string]bool
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	if !m["yes_val"] || m["no_val"] {
		t.Errorf("got %v", m)
	}
}

func TestUnmarshalEmptyFlowMapping(t *testing.T) {
	yaml := `
plugin: {}
`
	var m map[string]map[string]any
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	if m["plugin"] == nil {
		t.Error("want empty map, got nil")
	}
	if len(m["plugin"]) != 0 {
		t.Errorf("want empty map, got %v", m["plugin"])
	}
}

func TestUnmarshalDeepNesting(t *testing.T) {
	yaml := `
tasks:
  my-task:
    rss:
      url: "http://example.com/rss"
    regexp:
      accept:
        - "(?i)linux"
        - "(?i)open.?source"
    print:
`
	var m map[string]any
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	tasks, ok := m["tasks"].(map[string]any)
	if !ok {
		t.Fatalf("tasks not a map: %T", m["tasks"])
	}
	task, ok := tasks["my-task"].(map[string]any)
	if !ok {
		t.Fatalf("my-task not a map: %T", tasks["my-task"])
	}
	rss, ok := task["rss"].(map[string]any)
	if !ok {
		t.Fatalf("rss not a map: %T", task["rss"])
	}
	if rss["url"] != "http://example.com/rss" {
		t.Errorf("url: got %v", rss["url"])
	}

	regexp, ok := task["regexp"].(map[string]any)
	if !ok {
		t.Fatalf("regexp not a map")
	}
	accept, ok := regexp["accept"].([]any)
	if !ok {
		t.Fatalf("accept not a slice: %T", regexp["accept"])
	}
	if len(accept) != 2 {
		t.Errorf("accept: want 2 items, got %d", len(accept))
	}

	if task["print"] != nil {
		t.Errorf("print: want nil, got %v", task["print"])
	}
}

func TestUnmarshalEmptyDocument(t *testing.T) {
	var m map[string]any
	if err := Unmarshal([]byte(""), &m); err != nil {
		t.Fatal(err)
	}
	// nil YAML → null JSON → nil map
	if m != nil {
		t.Errorf("want nil, got %v", m)
	}
}

func TestUnmarshalIntoStruct(t *testing.T) {
	type Inner struct {
		URL string `json:"url"`
	}
	type Outer struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		Sub  Inner  `json:"sub"`
	}
	yaml := `
host: localhost
port: 8080
sub:
  url: "http://example.com"
`
	var out Outer
	if err := Unmarshal([]byte(yaml), &out); err != nil {
		t.Fatal(err)
	}
	if out.Host != "localhost" || out.Port != 8080 {
		t.Errorf("got %+v", out)
	}
	if out.Sub.URL != "http://example.com" {
		t.Errorf("sub.url: got %q", out.Sub.URL)
	}
}

func TestUnmarshalTemplateStrings(t *testing.T) {
	yaml := `
label: '{{.Title}}'
path: '/tv/{{.series_name}}/Season {{printf "%02d" .series_season}}'
`
	var m map[string]string
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	if m["label"] != "{{.Title}}" {
		t.Errorf("label: got %q", m["label"])
	}
	want := `/tv/{{.series_name}}/Season {{printf "%02d" .series_season}}`
	if m["path"] != want {
		t.Errorf("path: got %q", m["path"])
	}
}

func TestUnmarshalVariableTokens(t *testing.T) {
	yaml := `
url: "{$ feed_url $}"
db: "{$ db_path $}"
`
	var m map[string]string
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	if m["url"] != "{$ feed_url $}" {
		t.Errorf("url: got %q", m["url"])
	}
}

func TestUnmarshalInlineBlockMapping(t *testing.T) {
	// A sequence item may start a block mapping on the same line as "- ".
	// Subsequent key-value pairs at indent+2 belong to the same mapping item.
	yaml := `
items:
  - pattern: 'BluRay'
    from: source
  - pattern: 'HDTV'
    from: title
`
	var m map[string][]map[string]string
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	items := m["items"]
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d: %v", len(items), items)
	}
	if items[0]["pattern"] != "BluRay" || items[0]["from"] != "source" {
		t.Errorf("item[0]: got %v", items[0])
	}
	if items[1]["pattern"] != "HDTV" || items[1]["from"] != "title" {
		t.Errorf("item[1]: got %v", items[1])
	}
}

func TestUnmarshalInlineBlockMappingMixed(t *testing.T) {
	// Sequence with a mix of plain scalars and inline-mapping items.
	yaml := `
rules:
  - '(?i)linux'
  - pattern: 'BluRay'
    from: source
  - '(?i)windows'
`
	var m map[string][]any
	if err := Unmarshal([]byte(yaml), &m); err != nil {
		t.Fatal(err)
	}
	rules := m["rules"]
	if len(rules) != 3 {
		t.Fatalf("want 3 rules, got %d", len(rules))
	}
	if s, ok := rules[0].(string); !ok || s != "(?i)linux" {
		t.Errorf("rules[0]: got %v", rules[0])
	}
	mp, ok := rules[1].(map[string]any)
	if !ok {
		t.Fatalf("rules[1]: want map, got %T: %v", rules[1], rules[1])
	}
	if mp["pattern"] != "BluRay" || mp["from"] != "source" {
		t.Errorf("rules[1]: got %v", mp)
	}
	if s, ok := rules[2].(string); !ok || s != "(?i)windows" {
		t.Errorf("rules[2]: got %v", rules[2])
	}
}

func TestStripComment(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"key: value", "key: value"},
		{"key: value # comment", "key: value"},
		{"key: value#notcomment", "key: value#notcomment"},
		{`key: "url # not a comment"`, `key: "url # not a comment"`},
		{`key: 'url # not a comment'`, `key: 'url # not a comment'`},
	}
	for _, c := range cases {
		got := stripComment(c.in)
		if got != c.want {
			t.Errorf("stripComment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
