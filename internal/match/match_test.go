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
		// Incidental punctuation becomes a space so canonical titles match the
		// punctuation-stripped names scene releases use.
		{"Star Trek: Strange New Worlds", "star trek strange new worlds"},
		{"Marvel's Agents of S.H.I.E.L.D.", "marvels agents of s h i e l d"},
		{"Bob's Burgers", "bobs burgers"},
		{"Law & Order", "law order"},
		{"Whose Line Is It Anyway!", "whose line is it anyway"},
		// Glob metacharacters survive for Fuzzy's pattern matching.
		{"Star Wars*", "star wars*"},
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

// TestFuzzyPunctuatedTitle covers the real-world regression: a favorited show
// whose canonical name carries a colon ("Star Trek: Strange New Worlds") must
// match the colon-free name a scene torrent uses, once both are normalized.
func TestFuzzyPunctuatedTitle(t *testing.T) {
	cases := []struct{ canonical, torrent string }{
		{"Star Trek: Strange New Worlds", "Star Trek Strange New Worlds"},
		{"Bob's Burgers", "Bobs Burgers"},
		{"9-1-1: Lone Star", "9 1 1 Lone Star"},
	}
	for _, tc := range cases {
		if !Fuzzy(Normalize(tc.torrent), Normalize(tc.canonical)) {
			t.Errorf("Fuzzy(Normalize(%q), Normalize(%q)) = false, want true",
				tc.torrent, tc.canonical)
		}
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
