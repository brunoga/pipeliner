package plugin

import "testing"

func TestIntVal(t *testing.T) {
	cases := []struct {
		v    any
		def  int
		want int
	}{
		{42, 0, 42},
		{int64(42), 0, 42},
		{42.0, 0, 42},
		{"42", 7, 7},
		{nil, 7, 7},
	}
	for _, c := range cases {
		if got := IntVal(c.v, c.def); got != c.want {
			t.Errorf("IntVal(%v, %v) = %d, want %d", c.v, c.def, got, c.want)
		}
	}
}

func TestToStringSlice(t *testing.T) {
	if got := ToStringSlice("one"); len(got) != 1 || got[0] != "one" {
		t.Errorf("scalar string: %v", got)
	}
	if got := ToStringSlice([]string{"a", "b"}); len(got) != 2 {
		t.Errorf("[]string: %v", got)
	}
	if got := ToStringSlice([]any{"a", 1, "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("[]any drops non-strings: %v", got)
	}
	if got := ToStringSlice(42); got != nil {
		t.Errorf("non-string-like: %v", got)
	}
	if got := ToStringSlice(nil); got != nil {
		t.Errorf("nil: %v", got)
	}
}

func TestOptBool(t *testing.T) {
	if !OptBool(map[string]any{}, "x", true) {
		t.Error("default true should win when key absent")
	}
	if OptBool(map[string]any{"x": false}, "x", true) {
		t.Error("explicit false should override default")
	}
	if !OptBool(map[string]any{"x": "not a bool"}, "x", true) {
		t.Error("non-bool value should fall back to default")
	}
}
