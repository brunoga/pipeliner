package config

import (
	"strings"
	"testing"

	_ "github.com/brunoga/pipeliner/plugins/processor/filter/condition"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/route"
	_ "github.com/brunoga/pipeliner/plugins/sink/print"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
)

// Validate is what `pipeliner check` runs and what the visual editor's
// validate button calls. BuildTasks is what a daemon start and a config
// reload run. The first is the gate on the second, so a config the first
// passes and the second refuses is a gate that does not gate.
//
// They used to disagree about expressions: `condition` and `route` compiled
// theirs in the plugin factory, which only BuildTasks calls, so a rule like
// `probe_unreachable ==== true` printed "config OK" and was then refused by
// the reload it was checked for. These tests pin the agreement end to end,
// through the real Starlark loader rather than a hand-built config map.
func TestCheckRejectsWhatAReloadWouldReject(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "condition reject expression",
			src: `
src = input("rss", url="https://example.com/feed")
bad = process("condition", upstream=src, reject="video_year ==== 2020")
out = output("print", upstream=bad)
pipeline("p")
`,
			want: "condition",
		},
		{
			name: "condition expression inside rules",
			src: `
src = input("rss", url="https://example.com/feed")
bad = process("condition", upstream=src, rules=[
    {"reject": "source == \"CAM\""},
    {"reject": "probe_unreachable ==== true"},
])
out = output("print", upstream=bad)
pipeline("p")
`,
			want: "rules[1]",
		},
		{
			name: "route port accept expression",
			src: `
src = input("rss", url="https://example.com/feed")
r   = route(src,
    good = "video_year >= 2020",
    bad  = "video_year ==== 2019")
a = output("print", upstream=r.good)
b = output("print", upstream=r.bad)
pipeline("p")
`,
			want: "route",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseBytes([]byte(tc.src))
			if err != nil {
				t.Fatalf("ParseBytes: %v", err)
			}

			errs, _ := Validate(c)
			if len(errs) == 0 {
				t.Fatalf("Validate accepted a config with a broken expression — "+
					"`pipeliner check` would print %q", "config OK")
			}
			var found bool
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no error names %q; got %v", tc.want, errs)
			}

			// And the thing Validate is the gate for must agree.
			if _, err := BuildTasks(c, nil, nil); err == nil {
				t.Error("BuildTasks accepted it, so there was nothing to gate")
			}
		})
	}
}

// TestCheckAcceptsWhatAReloadAccepts is the other direction: the fix must not
// start refusing configs that load perfectly well.
func TestCheckAcceptsWhatAReloadAccepts(t *testing.T) {
	const src = `
src = input("rss", url="https://example.com/feed")
ok  = process("condition", upstream=src, rules=[
    {"reject": "video_year < 2000", "reason": "too old"},
    {"accept": "true", "reason": "everything else"},
])
r = route(ok,
    recent = "video_year >= 2020",
    rest   = "true")
a = output("print", upstream=r.recent)
b = output("print", upstream=r.rest)
pipeline("p")
`
	c, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if errs, _ := Validate(c); len(errs) != 0 {
		t.Fatalf("Validate rejected a good config: %v", errs)
	}
	if _, err := BuildTasks(c, nil, nil); err != nil {
		t.Fatalf("BuildTasks rejected a good config: %v", err)
	}
}
