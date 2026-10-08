package expr

import "testing"

// TestAlwaysTrue covers the literal-constant detection used to suppress the
// accept-only condition warning. The property that matters is the direction
// of the errors: reporting false for something universally true only leaves
// an advisory warning in place, while reporting true for something that can
// be false would suppress a real one.
func TestAlwaysTrue(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Literal constants that hold for every entry.
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"(true)", true},
		{"  true  ", true},
		{"((true))", true},
		{"1", true},
		{"42", true},
		{"-1", true},

		// Literal constants that do not.
		{"false", false},
		{"FALSE", false},
		{"(false)", false},
		{"0", false},

		// Not literals: these depend on the entry, or would need the parser
		// to reason about the expression. Both report false.
		{"video_rating >= 7.0", false},
		{"1 == 1", false},
		{"true and true", false},
		{"not false", false},
		{`title == "x"`, false},

		// Template form — opaque until rendered.
		{"{{if .Fields}}true{{end}}", false},
	}

	for _, c := range cases {
		e, err := Compile(c.in)
		if err != nil {
			t.Errorf("Compile(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got := e.AlwaysTrue(); got != c.want {
			t.Errorf("Compile(%q).AlwaysTrue() = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestAlwaysTrueNil guards the nil receiver, since callers reach this from a
// config map where the expression may be absent.
func TestAlwaysTrueNil(t *testing.T) {
	var e *Expr
	if e.AlwaysTrue() {
		t.Error("nil *Expr reported AlwaysTrue")
	}
}

// TestAlwaysTrueAgreesWithEval checks the constants against the evaluator, so
// the two cannot drift: anything reported always-true must in fact evaluate
// true, against an empty entry and against a populated one.
func TestAlwaysTrueAgreesWithEval(t *testing.T) {
	for _, in := range []string{"true", "TRUE", "(true)", "1", "42", "-1"} {
		e, err := Compile(in)
		if err != nil {
			t.Fatalf("Compile(%q): %v", in, err)
		}
		if !e.AlwaysTrue() {
			t.Fatalf("Compile(%q).AlwaysTrue() = false, want true", in)
		}
		for _, data := range []map[string]any{
			{},
			{"title": "x", "video_rating": 9.1},
		} {
			ok, err := e.Eval(data)
			if err != nil {
				t.Errorf("Eval(%q, %v): %v", in, data, err)
				continue
			}
			if !ok {
				t.Errorf("Eval(%q, %v) = false, but AlwaysTrue() said true", in, data)
			}
		}
	}
}
