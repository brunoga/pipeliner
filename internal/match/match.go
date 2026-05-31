// Package match provides fuzzy title-matching utilities shared across filter plugins.
// It also defines the TitleEntry type and year-compatibility helpers used by
// movies, series, and trakt filters for year-aware matching.
package match

import (
	"path/filepath"
	"strings"
)

// Normalize lowercases s and collapses dots, underscores, hyphens, and
// repeated spaces into single spaces, suitable for title comparison.
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

// Fuzzy returns true if a and b represent the same title. After normalization
// by the caller, a must either equal b exactly or match b as a filepath.Match
// glob pattern. Edit-distance tolerance is deliberately not applied: a single
// character can flip a title's meaning ("Master"/"Masters", "Saw"/"Jaws") and
// silent wrong-matches are worse than a no-match the user can see and fix.
func Fuzzy(a, b string) bool {
	if a == b {
		return true
	}
	matched, err := filepath.Match(b, a)
	return err == nil && matched
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

// FuzzyEntry returns true when norm matches t.Norm (exact or glob) AND the
// years are compatible. It is the standard entry-point for media title
// matching across the filter plugins.
func FuzzyEntry(norm string, year int, t TitleEntry) bool {
	return Fuzzy(norm, t.Norm) && YearsCompatible(year, t.Year)
}
