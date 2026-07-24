package config

import (
	"strings"
	"testing"
)

func TestParseAfter(t *testing.T) {
	for _, tc := range []struct{ in, parent, cond string; wantErr bool }{
		{"a", "a", "", false},
		{"a:accepted", "a", "accepted", false},
		{":accepted", "", "", true},
		{"a:bogus", "", "", true},
	} {
		p, c, err := ParseAfter(tc.in)
		if (err != nil) != tc.wantErr || p != tc.parent || c != tc.cond {
			t.Errorf("ParseAfter(%q) = %q,%q,%v", tc.in, p, c, err)
		}
	}
}

func TestValidateAfter(t *testing.T) {
	cfg := &Config{GraphAfter: map[string]string{"b": "a"}}
	if errs := validateAfter(cfg); len(errs) != 1 || !strings.Contains(errs[0].Error(), "unknown pipeline") {
		t.Errorf("unknown parent: %v", errs)
	}
	cfg = &Config{GraphAfter: map[string]string{"a": "a"}}
	if errs := validateAfter(cfg); len(errs) != 1 || !strings.Contains(errs[0].Error(), "itself") {
		t.Errorf("self: %v", errs)
	}
	cfg = &Config{GraphAfter: map[string]string{"a": "b", "b": "a"}}
	if errs := validateAfter(cfg); len(errs) < 2 { // unknown-graph x2 skipped; cycle detected from both sides
		t.Errorf("cycle: %v", errs)
	}
}

func TestDependents(t *testing.T) {
	after := map[string]string{"b": "a", "c": "a:accepted", "d": "x"}
	got := Dependents(after, "a", 0)
	if len(got) != 1 || got[0] != "b" {
		t.Errorf("accepted=0: %v", got)
	}
	got = Dependents(after, "a", 3)
	if len(got) != 2 {
		t.Errorf("accepted=3: %v", got)
	}
	if got := Dependents(after, "zzz", 1); len(got) != 0 {
		t.Errorf("no dependents: %v", got)
	}
}

func TestPipelineAfterParsesEndToEnd(t *testing.T) {
	cfg, err := ParseBytes([]byte(`
src = input("rss", url="https://example.com/rss")
out = output("print", upstream=src)
pipeline("a", schedule="1h")

src2 = input("rss", url="https://example.com/rss2")
out2 = output("print", upstream=src2)
pipeline("b", after="a:accepted")
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GraphAfter["b"] != "a:accepted" {
		t.Fatalf("GraphAfter: %+v", cfg.GraphAfter)
	}
	if errs, _ := Validate(cfg); len(errs) != 0 {
		t.Fatalf("validate: %v", errs)
	}

	// Bad reference surfaces as a validation error.
	cfg2, err := ParseBytes([]byte(`
src = input("rss", url="https://example.com/rss")
out = output("print", upstream=src)
pipeline("only", after="ghost")
`))
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := Validate(cfg2)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "unknown pipeline") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want unknown-pipeline error, got %v", errs)
	}
}
