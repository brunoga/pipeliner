// Package yaml is a minimal YAML parser that handles the subset of YAML used
// by pipeliner config files. It supports block mappings, block sequences,
// and scalar values (null, bool, int, float, quoted/unquoted strings).
// It does not support anchors/aliases, multi-document streams, or flow
// collections beyond empty {} and [].
//
// Parsing is done by converting the YAML document to a Go value tree (any),
// then round-tripping through encoding/json to populate typed target structs.
// Mappings are represented as [orderedMap] so that key order is preserved
// through the JSON round-trip.
package yaml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Unmarshal parses YAML data and populates v via a JSON round-trip.
// The target type must be JSON-compatible.
func Unmarshal(data []byte, v any) error {
	val, err := parseDocument(data)
	if err != nil {
		return fmt.Errorf("yaml: %w", err)
	}
	j, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("yaml: marshal: %w", err)
	}
	if err := json.Unmarshal(j, v); err != nil {
		return fmt.Errorf("yaml: unmarshal: %w", err)
	}
	return nil
}

// --- internal types ---

// orderedMap is an ordered sequence of key–value pairs that marshals to a
// JSON object with keys in insertion order.
type orderedMap []pair

type pair struct {
	key string
	val any
}

func (m orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, p := range m {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(p.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(p.val)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

type yamlLine struct {
	indent int
	text   string
}

type parser struct {
	lines []yamlLine
	pos   int
}

// --- document parsing ---

func parseDocument(data []byte) (any, error) {
	lines := tokenize(string(data))
	if len(lines) == 0 {
		return nil, nil
	}
	p := &parser{lines: lines}
	return p.parseNode(-1)
}

func tokenize(text string) []yamlLine {
	var out []yamlLine
	for raw := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		indent := leadingSpaces(raw)
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		cleaned := stripComment(raw[indent:])
		cleaned = strings.TrimRight(cleaned, " \t")
		if cleaned == "" {
			continue
		}
		out = append(out, yamlLine{indent: indent, text: cleaned})
	}
	return out
}

func leadingSpaces(s string) int {
	for i, c := range s {
		if c != ' ' {
			return i
		}
	}
	return len(s)
}

// stripComment removes a trailing " # comment" from a line while respecting
// quoted strings.
func stripComment(s string) string {
	inDouble := false
	inSingle := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '#' && !inDouble && !inSingle && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t'):
			return strings.TrimRight(s[:i], " \t")
		}
	}
	return s
}

// --- recursive parser ---

func (p *parser) peek() *yamlLine {
	if p.pos >= len(p.lines) {
		return nil
	}
	return &p.lines[p.pos]
}

// parseNode parses a block node whose indent must be strictly greater than
// parentIndent. Returns nil if there is no such node.
func (p *parser) parseNode(parentIndent int) (any, error) {
	l := p.peek()
	if l == nil || l.indent <= parentIndent {
		return nil, nil
	}
	nodeIndent := l.indent

	// Sequence indicator
	if l.text == "-" || strings.HasPrefix(l.text, "- ") {
		return p.parseSequence(nodeIndent)
	}

	// Key-value pair → mapping
	if _, _, ok := splitKey(l.text); ok {
		return p.parseMapping(nodeIndent)
	}

	// Bare scalar
	p.pos++
	return parseScalar(l.text), nil
}

func (p *parser) parseMapping(indent int) (orderedMap, error) {
	var m orderedMap
	for {
		l := p.peek()
		if l == nil || l.indent != indent {
			break
		}
		key, valText, ok := splitKey(l.text)
		if !ok {
			break
		}
		p.pos++

		var val any
		switch valText {
		case "", "null", "~":
			next := p.peek()
			if next != nil && next.indent > indent {
				var err error
				val, err = p.parseNode(indent)
				if err != nil {
					return nil, fmt.Errorf("key %q: %w", key, err)
				}
			}
		case "{}":
			val = orderedMap{}
		case "[]":
			val = []any{}
		default:
			val = parseScalar(valText)
		}
		m = append(m, pair{key, val})
	}
	return m, nil
}

// parseInlineMapping parses a block mapping where the first key-value pair was
// already extracted from the same line as a sequence "- " marker. Subsequent
// key-value pairs at the given indent level are consumed from the token stream.
func (p *parser) parseInlineMapping(firstKey, firstValText string, indent int) (orderedMap, error) {
	var m orderedMap
	var firstVal any
	switch firstValText {
	case "", "null", "~":
		next := p.peek()
		if next != nil && next.indent > indent {
			var err error
			firstVal, err = p.parseNode(indent)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", firstKey, err)
			}
		}
	default:
		firstVal = parseScalar(firstValText)
	}
	m = append(m, pair{firstKey, firstVal})
	for {
		l := p.peek()
		if l == nil || l.indent != indent {
			break
		}
		key, valText, ok := splitKey(l.text)
		if !ok {
			break
		}
		p.pos++
		var val any
		switch valText {
		case "", "null", "~":
			next := p.peek()
			if next != nil && next.indent > indent {
				var err error
				val, err = p.parseNode(indent)
				if err != nil {
					return nil, fmt.Errorf("key %q: %w", key, err)
				}
			}
		default:
			val = parseScalar(valText)
		}
		m = append(m, pair{key, val})
	}
	return m, nil
}

func (p *parser) parseSequence(indent int) ([]any, error) {
	var seq []any
	for {
		l := p.peek()
		if l == nil || l.indent != indent {
			break
		}
		var itemText string
		if l.text == "-" {
			itemText = ""
		} else if strings.HasPrefix(l.text, "- ") {
			itemText = l.text[2:]
		} else {
			break
		}
		p.pos++

		var item any
		switch itemText {
		case "", "null", "~":
			next := p.peek()
			if next != nil && next.indent > indent {
				var err error
				item, err = p.parseNode(indent)
				if err != nil {
					return nil, err
				}
			}
		default:
			// Check for an inline block-mapping item: "- key: val\n  key2: val2".
			// The mapping content sits at indent+2 (the column after "- ").
			if key, val, ok := splitKey(itemText); ok {
				var err error
				item, err = p.parseInlineMapping(key, val, indent+2)
				if err != nil {
					return nil, err
				}
			} else {
				item = parseScalar(itemText)
			}
		}
		seq = append(seq, item)
	}
	return seq, nil
}

// --- helpers ---

// splitKey splits "key: rest" into (key, rest, true).
// Returns ("", "", false) if the line is not a mapping entry.
func splitKey(s string) (key, val string, ok bool) {
	if s == "-" || strings.HasPrefix(s, "- ") {
		return "", "", false
	}
	inDouble := false
	inSingle := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == ':' && !inDouble && !inSingle:
			if i+1 == len(s) || s[i+1] == ' ' {
				rawKey := strings.TrimSpace(s[:i])
				key = unquote(rawKey)
				if i+2 < len(s) {
					val = strings.TrimSpace(s[i+2:])
				}
				return key, val, true
			}
		}
	}
	return "", "", false
}

// unquote strips surrounding quotes from a string, if present.
func unquote(s string) string {
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u
			}
			return s[1 : len(s)-1]
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parseScalar converts a raw YAML scalar string to the appropriate Go type.
func parseScalar(s string) any {
	if s == "" || s == "null" || s == "~" {
		return nil
	}
	// Quoted strings
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u
			}
			return s[1 : len(s)-1]
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return s[1 : len(s)-1]
		}
	}
	// Booleans (YAML 1.2 only)
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	// Integer
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	// Float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}
