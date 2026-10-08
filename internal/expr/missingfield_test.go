package expr

import "testing"

// TestMissingFieldComparesFalse pins the property that ordered reject rules
// depend on: a field that is absent from the entry compares false rather than
// erroring. A condition rule chain is first-match-wins, so a rule testing a
// field only some entries carry has to be safe to evaluate on the ones that
// do not — and pipeliner check never evaluates these expressions, so nothing
// else would catch a change here.
func TestMissingFieldComparesFalse(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"absent_field == true", false},
		{"absent_field == false", false},
		{`absent_field == "x"`, false},
		{"absent_field != true", true},
		{"absent_field > 0", false},
	}
	for _, c := range cases {
		e, err := Compile(c.src)
		if err != nil {
			t.Errorf("Compile(%q): %v", c.src, err)
			continue
		}
		got, err := e.Eval(map[string]any{"present": 1})
		if err != nil {
			t.Errorf("Eval(%q) on an entry without the field errored: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("Eval(%q) = %v, want %v", c.src, got, c.want)
		}
	}
}

// TestProbeVetoOrdering covers the three-way veto the MVC ambiguous lane uses
// to tell apart "the swarm is dead", "the probe did not finish" and "the
// probe read it and it is not MVC". probe_unreachable implies probe_ok ==
// false, so the order of the rules is what makes them distinguishable at all;
// probe sets probe_unreachable only for ErrNoPeers and leaves it absent
// otherwise.
func TestProbeVetoOrdering(t *testing.T) {
	const (
		accept      = `probe_ok == true and probe_3d_layout == "mvc"`
		unreachable = "probe_unreachable == true"
		notFinished = "probe_ok == false"
	)
	// firstMatch mirrors the plugin's first-match-wins evaluation, returning
	// which rule a given entry lands on.
	firstMatch := func(t *testing.T, data map[string]any) string {
		for _, r := range []struct{ name, src string }{
			{"accept", accept},
			{"unreachable", unreachable},
			{"not-finished", notFinished},
		} {
			e, err := Compile(r.src)
			if err != nil {
				t.Fatalf("Compile(%q): %v", r.src, err)
			}
			ok, err := e.Eval(data)
			if err != nil {
				t.Fatalf("Eval(%q, %v): %v", r.src, data, err)
			}
			if ok {
				return r.name
			}
		}
		return "catch-all"
	}

	cases := []struct {
		name string
		data map[string]any
		want string
	}{
		{"dead swarm", map[string]any{"probe_ok": false, "probe_unreachable": true}, "unreachable"},
		{"timed out", map[string]any{"probe_ok": false}, "not-finished"},
		{"read it, not mvc", map[string]any{"probe_ok": true, "probe_3d_layout": "half"}, "catch-all"},
		{"read it, mvc", map[string]any{"probe_ok": true, "probe_3d_layout": "mvc"}, "accept"},
		// A probe that never ran at all leaves every field absent.
		{"probe never ran", map[string]any{}, "catch-all"},
	}
	for _, c := range cases {
		if got := firstMatch(t, c.data); got != c.want {
			t.Errorf("%s: landed on %q, want %q", c.name, got, c.want)
		}
	}
}

// TestAmbiguousAcceptNeedsRealProbe guards the rule that makes the ambiguous
// lane safe: silence is not permission there, so the accept must never fire
// without a probe that actually read the container.
func TestAmbiguousAcceptNeedsRealProbe(t *testing.T) {
	e, err := Compile(`probe_ok == true and probe_3d_layout == "mvc"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []map[string]any{
		{"probe_ok": false, "probe_unreachable": true},
		{"probe_ok": false},
		{},
		// The layout claim alone is not enough without a successful probe.
		{"probe_3d_layout": "mvc"},
	} {
		ok, err := e.Eval(data)
		if err != nil {
			t.Errorf("Eval(%v): %v", data, err)
			continue
		}
		if ok {
			t.Errorf("Eval(%v) = true, but no probe vouched for it", data)
		}
	}
}
