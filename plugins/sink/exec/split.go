package exec

import (
	"fmt"
	"strings"
)

// splitArgs splits a rendered command string into argv elements, honouring
// single quotes, double quotes and backslash escapes.
//
// The old behaviour was strings.Fields, which made the sink unusable for the
// thing it is most often pointed at: a path. Media paths are full of spaces,
// so "convert /media/Life of Pi (2012)/x.mkv" arrived as six arguments and the
// command failed with no way for the config to say otherwise — quotes were
// passed through as literal characters, so quoting did not help either.
//
// Deliberately not a shell. There is no variable expansion, globbing, pipes,
// redirection or command substitution, because the rendered string contains
// release titles from an indexer and a shell would make those executable. For
// arguments that cannot be expressed by quoting, use the `args` list, where
// each element is one argv entry and no splitting happens at all.
//
// Quoting rules are the POSIX ones a reader would expect:
//   - inside single quotes every character is literal, including backslash
//   - inside double quotes a backslash escapes only " and \
//   - outside quotes a backslash escapes the next character
//   - quotes may open and close mid-word: a"b c"d is one argument, `ab cd`
func splitArgs(s string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		started bool // distinguishes an empty quoted "" from no argument at all
	)
	flush := func() {
		if started {
			args = append(args, cur.String())
			cur.Reset()
			started = false
		}
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			flush()
		case '\'':
			started = true
			j := i + 1
			for j < len(runes) && runes[j] != '\'' {
				cur.WriteRune(runes[j])
				j++
			}
			if j >= len(runes) {
				return nil, fmt.Errorf("unterminated single quote")
			}
			i = j
		case '"':
			started = true
			j := i + 1
			for j < len(runes) && runes[j] != '"' {
				if runes[j] == '\\' && j+1 < len(runes) &&
					(runes[j+1] == '"' || runes[j+1] == '\\') {
					j++
				}
				cur.WriteRune(runes[j])
				j++
			}
			if j >= len(runes) {
				return nil, fmt.Errorf("unterminated double quote")
			}
			i = j
		case '\\':
			if i+1 < len(runes) {
				started = true
				i++
				cur.WriteRune(runes[i])
				continue
			}
			return nil, fmt.Errorf("trailing backslash")
		default:
			started = true
			cur.WriteRune(c)
		}
	}
	flush()
	return args, nil
}
