package match

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Breaking Bad", "breaking bad"},
		{"Breaking.Bad", "breaking bad"},
		{"Breaking_Bad", "breaking bad"},
		{"Breaking-Bad", "breaking bad"},
		{"BREAKING  BAD", "breaking bad"}, // collapsed spaces
		{"  leading ", "leading"},
		{"The.Wire.S01E01", "the wire s01e01"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFuzzyExact(t *testing.T) {
	if !Fuzzy("breaking bad", "breaking bad") {
		t.Error("exact match should be true")
	}
}

func TestFuzzyGlob(t *testing.T) {
	if !Fuzzy("breaking bad", "breaking *") {
		t.Error("glob match should be true")
	}
}

// TestFuzzyRejectsEditDistance documents that single-character differences are
// not tolerated. The historical Levenshtein-≤1 rule produced silent
// wrong-matches like "Masters of the Universe" ↔ "Master of the Universe".
func TestFuzzyRejectsEditDistance(t *testing.T) {
	cases := []struct{ a, b string }{
		{"masters of the universe", "master of the universe"}, // plural vs singular
		{"breaking bad", "breking bad"},                       // deletion
		{"breaking bad", "braaking bad"},                      // insertion
		{"breaking bad", "bxeaking bad"},                      // substitution
	}
	for _, tc := range cases {
		if Fuzzy(tc.a, tc.b) {
			t.Errorf("Fuzzy(%q, %q) should be false — single-edit titles must not match", tc.a, tc.b)
		}
	}
}

func TestFuzzyDifferentTitle(t *testing.T) {
	if Fuzzy("breaking bad", "the wire") {
		t.Error("completely different titles should not match")
	}
}

func TestFuzzySequel(t *testing.T) {
	// A sequel (extra word) must not match the original.
	if Fuzzy("the dark knight", "the dark knight rises") {
		t.Error("sequel should not match original")
	}
}

func TestFuzzyEmpty(t *testing.T) {
	if !Fuzzy("", "") {
		t.Error("two empty strings should match")
	}
	if Fuzzy("", "something") {
		t.Error("empty vs non-empty should not match")
	}
}
