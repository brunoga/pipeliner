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

func TestFuzzyOneTypo(t *testing.T) {
	cases := []struct{ a, b string }{
		{"breaking bad", "breking bad"},   // deletion
		{"breaking bad", "braaking bad"},  // insertion
		{"breaking bad", "bXeaking bad"},  // substitution
	}
	for _, tc := range cases {
		if !Fuzzy(tc.a, tc.b) {
			t.Errorf("Fuzzy(%q, %q) should be true (single edit)", tc.a, tc.b)
		}
	}
}

func TestFuzzyTwoEdits(t *testing.T) {
	// Two edits should not match.
	if Fuzzy("breaking bad", "brXking bXd") {
		t.Error("two-edit distance should not match")
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

func TestFuzzyShortTitleNoFuzzy(t *testing.T) {
	// Short titles (< minFuzzyLen chars) must not fuzzy-match similar-looking words.
	// "junge" (5) is within edit-distance 1 of "jungle" (6) but they are unrelated.
	if Fuzzy("junge", "jungle") {
		t.Error(`Fuzzy("junge", "jungle") should be false — short title must not fuzzy-match`)
	}
	// Exact match still works for short titles.
	if !Fuzzy("jaws", "jaws") {
		t.Error(`Fuzzy("jaws", "jaws") should be true — exact match always works`)
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

func TestLevenshteinSameString(t *testing.T) {
	if d := levenshtein("abc", "abc"); d != 0 {
		t.Errorf("same string: got %d, want 0", d)
	}
}

func TestLevenshteinInsertion(t *testing.T) {
	if d := levenshtein("abc", "abcd"); d != 1 {
		t.Errorf("one insertion: got %d, want 1", d)
	}
}

func TestLevenshteinDeletion(t *testing.T) {
	if d := levenshtein("abcd", "abc"); d != 1 {
		t.Errorf("one deletion: got %d, want 1", d)
	}
}

func TestLevenshteinSubstitution(t *testing.T) {
	if d := levenshtein("abc", "aXc"); d != 1 {
		t.Errorf("one substitution: got %d, want 1", d)
	}
}

func TestLevenshteinEmptyStrings(t *testing.T) {
	if d := levenshtein("", "abc"); d != 3 {
		t.Errorf("empty vs 3-char: got %d, want 3", d)
	}
	if d := levenshtein("abc", ""); d != 3 {
		t.Errorf("3-char vs empty: got %d, want 3", d)
	}
}

func TestLevenshteinEarlyExit(t *testing.T) {
	// Length difference > 5 should short-circuit.
	d := levenshtein("a", "abcdefgh")
	if d <= 5 {
		t.Errorf("large length diff should return high value, got %d", d)
	}
}
