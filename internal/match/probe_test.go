package match

import "testing"

func list(titles ...string) []TitleEntry {
	l := make([]TitleEntry, len(titles))
	for i, t := range titles {
		l[i] = NewTitleEntry(t, 0)
	}
	return l
}

func TestProbeMatch(t *testing.T) {
	// The canonical case: a scene title matches a punctuated favorite because
	// Normalize folds the colon on both sides.
	res := Probe("Star Trek Strange New Worlds", 0,
		list("Silo", "Star Trek: Strange New Worlds", "The Ark"))
	if !res.Matched {
		t.Fatalf("expected match, got none (input norm %q)", res.InputNorm)
	}
	if res.MatchedBy != "star trek strange new worlds" {
		t.Errorf("MatchedBy = %q", res.MatchedBy)
	}
	if res.InputNorm != "star trek strange new worlds" {
		t.Errorf("InputNorm = %q", res.InputNorm)
	}
	// The matching candidate must sort first.
	if !res.Candidates[0].Matched {
		t.Errorf("expected matched candidate first, got %+v", res.Candidates[0])
	}
}

func TestProbeNoMatchRanksNearest(t *testing.T) {
	res := Probe("Breaking Bad", 0, list("Better Call Saul", "Breaking Good", "The Wire"))
	if res.Matched {
		t.Fatalf("expected no match")
	}
	// "Breaking Good" (distance 2) should rank ahead of the others.
	if got := res.Candidates[0].Norm; got != "breaking good" {
		t.Errorf("nearest candidate = %q, want breaking good", got)
	}
	if res.Candidates[0].Distance != 3 {
		t.Errorf("distance = %d, want 3", res.Candidates[0].Distance)
	}
}

func TestProbePunctuationOnlyFlag(t *testing.T) {
	// Simulate a candidate that normalizes to a punctuation-carrying form by
	// constructing the TitleEntry with a pre-normalized-looking Norm. Because
	// Normalize now folds punctuation, we craft the mismatch directly on the
	// Norm field to model a hypothetical un-folded list source.
	cand := TitleEntry{Norm: "star trek strange new worlds", Year: 0}
	other := TitleEntry{Norm: "star trek: strange new worlds", Year: 0} // colon survived
	res := Probe("Star Trek Strange New Worlds", 0, []TitleEntry{other})
	_ = cand
	if res.Matched {
		t.Fatalf("colon-carrying candidate should not match")
	}
	if !res.Candidates[0].PunctuationOnly {
		t.Errorf("expected PunctuationOnly=true for %q vs %q", res.InputNorm, res.Candidates[0].Norm)
	}
}

func TestProbeYearGate(t *testing.T) {
	// Titles match but years are incompatible: Matched false, TitleMatched true.
	res := Probe("Dune", 2021, []TitleEntry{{Norm: "dune", Year: 1984}})
	if res.Matched {
		t.Fatalf("expected year gate to block match")
	}
	c := res.Candidates[0]
	if !c.TitleMatched {
		t.Errorf("TitleMatched should be true (titles are identical)")
	}
	if c.Matched {
		t.Errorf("Matched should be false (year mismatch)")
	}
	if c.Distance != 0 {
		t.Errorf("distance for identical titles = %d, want 0", c.Distance)
	}
}

func TestProbeYearCompatible(t *testing.T) {
	res := Probe("Dune", 2021, []TitleEntry{{Norm: "dune", Year: 2020}})
	if !res.Matched {
		t.Fatalf("years within tolerance should match")
	}
}

func TestProbeGlobPattern(t *testing.T) {
	res := Probe("Star Wars The Clone Wars", 0, list("Star Wars*"))
	if !res.Matched {
		t.Fatalf("glob pattern should match")
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"café", "cafe", 1}, // rune-aware
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestProbeEmptyList(t *testing.T) {
	res := Probe("Anything", 0, nil)
	if res.Matched || len(res.Candidates) != 0 {
		t.Errorf("empty list should yield no match and no candidates")
	}
	if res.InputNorm != "anything" {
		t.Errorf("InputNorm = %q", res.InputNorm)
	}
}
