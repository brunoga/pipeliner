package series

import "testing"

func TestCanonicalEpisodeID(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"S04E05", "S04E05", true},
		{"s4e5", "S04E05", true},
		{"S4E5", "S04E05", true},
		{"4x05", "S04E05", true},
		{"4x5", "S04E05", true},
		{"EP12", "EP012", true},
		{"ep012", "EP012", true},
		{"2023-11-15", "2023-11-15", true},
		{"  S04E05  ", "S04E05", true},
		{"", "", false},
		{"garbage", "", false},
		{"2023/11/15", "", false},
	}
	for _, c := range cases {
		got, ok := CanonicalEpisodeID(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("CanonicalEpisodeID(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
