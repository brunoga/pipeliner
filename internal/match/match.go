// Package match provides fuzzy title-matching utilities shared across filter plugins.
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

// Fuzzy returns true if a and b represent the same title after normalization.
// It accepts an exact match, a glob pattern match (b as the pattern), or a
// Levenshtein distance of at most 1 (single-character typo tolerance).
func Fuzzy(a, b string) bool {
	if a == b {
		return true
	}
	if matched, err := filepath.Match(b, a); err == nil && matched {
		return true
	}
	return levenshtein(a, b) <= 1
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
