// Package match provides fuzzy title-matching utilities shared across filter plugins.
// It also defines the TitleEntry type and year-compatibility helpers used by
// movies, series, and trakt filters for year-aware matching.
package match

import (
	"math"
	"path/filepath"
	"strings"
)

// Normalize lowercases s and collapses dots, underscores, hyphens, and
// repeated spaces into single spaces, suitable for fuzzy comparison.
func Normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r == '.' || r == '_' || r == '-' {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// minFuzzyLen is the minimum length both titles must have before Levenshtein
// fuzzy matching is attempted. Short titles have too high a false-positive rate
// (e.g. "junge" matching "jungle") so they require an exact or glob match only.
const minFuzzyLen = 6

// Fuzzy returns true if a and b represent the same title after normalization.
// It accepts an exact match, a glob pattern match (b as the pattern), or a
// Levenshtein distance of at most 1 (single-character typo tolerance) — but
// only when both titles are at least minFuzzyLen characters long.
func Fuzzy(a, b string) bool {
	if a == b {
		return true
	}
	if matched, err := filepath.Match(b, a); err == nil && matched {
		return true
	}
	if len(a) < minFuzzyLen || len(b) < minFuzzyLen {
		return false
	}
	return levenshtein(a, b) <= 1
}

// TitleEntry holds a normalised title and optional release year.
// Year 0 means unknown; YearsCompatible treats unknown years as always compatible.
type TitleEntry struct {
	Norm string
	Year int
}

// NewTitleEntry creates a TitleEntry from a raw title and year.
func NewTitleEntry(title string, year int) TitleEntry {
	return TitleEntry{Norm: Normalize(title), Year: year}
}

// YearsCompatible returns true when a and b are close enough to indicate the
// same film or show. Either year being 0 means unknown — the match is allowed
// rather than over-rejecting entries that lack year metadata. When both are
// known they must be within 1 of each other (off-by-one for regional releases,
// digital vs theatrical window differences, etc.).
func YearsCompatible(a, b int) bool {
	if a == 0 || b == 0 {
		return true
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= 1
}

// FuzzyEntry returns true when norm fuzzy-matches t.Norm AND the years are
// compatible. It is the standard entry-point for media title matching across
// the filter plugins.
func FuzzyEntry(norm string, year int, t TitleEntry) bool {
	return Fuzzy(norm, t.Norm) && YearsCompatible(year, t.Year)
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	if math.Abs(float64(la-lb)) > 5 {
		return 99
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
