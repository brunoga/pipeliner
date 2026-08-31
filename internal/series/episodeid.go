package series

import (
	"regexp"
	"strings"
)

var reDateID = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)

// CanonicalEpisodeID normalizes a user-supplied episode identifier to the
// stored canonical form so a manually-created tracker record keys identically
// to one the pipeline would write. It accepts:
//
//   - standard season/episode ("S4E5", "s04e05", "4x05") → "S04E05"
//   - absolute ("EP12", "ep012")                          → "EP012"
//   - date ("2023-11-15")                                 → "2023-11-15"
//
// The bool is false for anything it cannot recognise, so callers can reject the
// input instead of writing a key that will never be looked up.
func CanonicalEpisodeID(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if reDateID.MatchString(raw) {
		return raw, true
	}
	if s, e, ok := ParseEpisodeID(raw); ok {
		return EpisodeID(&Episode{Season: s, Episode: e}), true
	}
	// ParseEpisodeID does not accept the "4x05" spelling; try it explicitly.
	if m := reCrossSeason.FindStringSubmatch(raw); m != nil {
		return EpisodeID(&Episode{Season: atoi(m[1]), Episode: atoi(m[2])}), true
	}
	return "", false
}

var reCrossSeason = regexp.MustCompile(`^(\d{1,2})x(\d{1,3})$`)

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
