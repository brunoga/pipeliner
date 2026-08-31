package dag

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/brunoga/pipeliner/internal/match"
)

// globPatternListKeys maps each plugin whose static title list is matched with
// match.Fuzzy (i.e. filepath.Match) to the config key holding that list. Only
// these keys are scanned for glob footguns; a new plugin that matches static
// titles as patterns should be added here.
var globPatternListKeys = map[string]string{
	"series": "static",
	"movies": "static",
}

// globLintWarnings appends advisory warnings for static list entries that carry
// glob metacharacters the user likely did not intend. Since match.Normalize now
// deliberately preserves the filepath.Match metacharacters (* ? [ ] \) so that
// intentional patterns like "Star Wars*" keep working, a title with a stray '?'
// (e.g. a movie literally titled "Who Framed Roger Rabbit?") or an unbalanced
// '[' silently turns into a wildcard pattern. This surfaces that at config-load
// time instead of as a mysterious non-match at runtime.
//
// Static-only: it inspects the Starlark config, never runtime matches. '*' is
// deliberately not flagged — prefix/suffix wildcards are a documented feature
// and warning on them would just be noise.
func globLintWarnings(n *Node, warnings *[]error) {
	key, ok := globPatternListKeys[n.PluginName]
	if !ok {
		return
	}
	items, ok := n.Config[key].([]any)
	if !ok {
		return
	}
	for _, it := range items {
		raw, ok := it.(string)
		if !ok || raw == "" {
			continue
		}
		norm := match.Normalize(raw)
		if norm == "" {
			continue
		}
		// A malformed pattern (e.g. an unclosed '[') never matches anything —
		// the strongest footgun, reported distinctly.
		if _, err := filepath.Match(norm, ""); err != nil {
			*warnings = append(*warnings, fmt.Errorf(
				"node %q (plugin %q): %s entry %q is not a valid glob pattern (%v) and will never match — escape the metacharacters with \\ if they are literal",
				n.ID, n.PluginName, key, raw, err))
			continue
		}
		if chars := accidentalGlobChars(norm); len(chars) > 0 {
			*warnings = append(*warnings, fmt.Errorf(
				"node %q (plugin %q): %s entry %q contains glob metacharacter(s) %s — it is matched as a wildcard pattern; if they are literal, escape them with \\",
				n.ID, n.PluginName, key, raw, strings.Join(chars, " ")))
		}
	}
}

// accidentalGlobChars returns the distinct unescaped glob metacharacters in
// pattern that a user is unlikely to have intended in a title: '?' (single-char
// wildcard) and '[' (character-class open). A lone ']' is literal to
// filepath.Match, and '*' is an intentional feature, so neither is flagged.
func accidentalGlobChars(pattern string) []string {
	seen := map[rune]bool{}
	var out []string
	escaped := false
	for _, r := range pattern {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if (r == '?' || r == '[') && !seen[r] {
			seen[r] = true
			out = append(out, string(r))
		}
	}
	return out
}
