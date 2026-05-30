// Package interp provides string interpolation using {field} and {field:fmt} syntax.
//
// A pattern like:
//
//	/media/tv/{series_name}/Season {series_season:02d}
//
// is equivalent to the Go template:
//
//	/media/tv/{{.series_name}}/Season {{printf "%02d" .series_season}}
//
// Rules:
//   - {field_name}        → value of field_name from the data map
//   - {field_name:format} → fmt.Sprintf("%format", field_name)
//   - A literal { that is not followed by a valid identifier is passed through unchanged.
//
// Patterns that contain "{{" are compiled as Go templates directly, providing
// backward compatibility with the old {{.field}} syntax.
package interp

import (
	"bytes"
	"fmt"
	"maps"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/brunoga/pipeliner/internal/entry"
	itpl "github.com/brunoga/pipeliner/internal/template"
)

// Interpolator renders a pattern string against a data map.
type Interpolator struct {
	tmpl *template.Template
}

// Compile parses a pattern and returns an Interpolator.
// Patterns containing "{{" are compiled as Go templates directly.
func Compile(pattern string) (*Interpolator, error) {
	src := toGoTemplate(pattern)
	tmpl, err := template.New("").Funcs(itpl.FuncMap()).Parse(src)
	if err != nil {
		return nil, err
	}
	return &Interpolator{tmpl: tmpl}, nil
}

// FieldRefs returns the names of entry fields referenced in this pattern.
// Only top-level field identifiers are returned: for `{{.foo.bar}}` the result
// is "foo". Order matches first appearance; each name is returned at most once.
// Used for static analysis — e.g. detecting references to deprecated fields.
func (ip *Interpolator) FieldRefs() []string {
	seen := make(map[string]bool)
	var refs []string
	visit := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		refs = append(refs, name)
	}
	for _, t := range ip.tmpl.Templates() {
		if t.Tree == nil || t.Tree.Root == nil {
			continue
		}
		walkParseNode(t.Tree.Root, visit)
	}
	return refs
}

func walkParseNode(n parse.Node, visit func(string)) {
	switch v := n.(type) {
	case *parse.ListNode:
		if v == nil {
			return
		}
		for _, c := range v.Nodes {
			walkParseNode(c, visit)
		}
	case *parse.ActionNode:
		walkPipeNode(v.Pipe, visit)
	case *parse.IfNode:
		walkPipeNode(v.Pipe, visit)
		walkParseNode(v.List, visit)
		walkParseNode(v.ElseList, visit)
	case *parse.RangeNode:
		walkPipeNode(v.Pipe, visit)
		walkParseNode(v.List, visit)
		walkParseNode(v.ElseList, visit)
	case *parse.WithNode:
		walkPipeNode(v.Pipe, visit)
		walkParseNode(v.List, visit)
		walkParseNode(v.ElseList, visit)
	}
}

func walkPipeNode(p *parse.PipeNode, visit func(string)) {
	if p == nil {
		return
	}
	for _, cmd := range p.Cmds {
		for _, arg := range cmd.Args {
			switch a := arg.(type) {
			case *parse.FieldNode:
				if len(a.Ident) > 0 {
					visit(a.Ident[0])
				}
			case *parse.PipeNode:
				walkPipeNode(a, visit)
			}
		}
	}
}

// Render executes the interpolator against data and returns the result.
func (ip *Interpolator) Render(data map[string]any) (string, error) {
	var buf bytes.Buffer
	if err := ip.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// EntryData builds the template data map for a single entry.
//
// Key naming:
//   - Capitalised names (Title, URL, …) are kept for backward compatibility
//     with the old {{.Title}} template syntax and always refer to the raw
//     entry values set at input time.
//   - "raw_title" is the canonical lowercase name for the raw entry title
//     (the torrent filename or feed item title before any metainfo enrichment).
//   - "title" and other standard field names are populated from e.Fields,
//     which metainfo plugins fill via SetGenericInfo / SetVideoInfo / etc.
//     If no metainfo plugin has run, these keys are absent.
//
// e.Fields is merged last so standard fields always win over the built-in
// aliases above.
func EntryData(e *entry.Entry) map[string]any {
	m := map[string]any{
		// Capitalized — backward compat with {{.Title}} syntax.
		"Title":       e.Title,
		"URL":         e.URL,
		"OriginalURL": e.OriginalURL,
		"Task":        e.Task,
		// Lowercase built-ins.
		"raw_title":    e.Title,
		"url":          e.URL,
		"original_url": e.OriginalURL,
		"task":         e.Task,
	}
	maps.Copy(m, e.Fields)
	return m
}

// EntryDataWithState is like EntryData but also includes State and RejectReason.
func EntryDataWithState(e *entry.Entry) map[string]any {
	m := EntryData(e)
	state := e.State.String()
	m["State"] = state
	m["RejectReason"] = e.RejectReason
	m["state"] = state
	m["reject_reason"] = e.RejectReason
	return m
}

// toGoTemplate converts {field} / {field:fmt} syntax to Go template syntax.
// Strings already containing "{{" are returned unchanged.
func toGoTemplate(s string) string {
	if strings.Contains(s, "{{") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	i := 0
	for i < len(s) {
		if s[i] != '{' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Find closing brace on the same line.
		j := i + 1
		for j < len(s) && s[j] != '}' && s[j] != '\n' {
			j++
		}
		if j >= len(s) || s[j] != '}' {
			// No matching } — emit { literally.
			b.WriteByte('{')
			i++
			continue
		}
		inner := s[i+1 : j]
		// {field:format} → {{printf "%format" .field}}
		if colon := strings.IndexByte(inner, ':'); colon > 0 {
			field := inner[:colon]
			format := inner[colon+1:]
			if isIdent(field) && format != "" {
				fmt.Fprintf(&b, `{{printf "%%%s" .%s}}`, format, field)
				i = j + 1
				continue
			}
		}
		// {field} → {{.field}}
		if isIdent(inner) {
			fmt.Fprintf(&b, "{{.%s}}", inner)
			i = j + 1
			continue
		}
		// Not a valid field reference — emit { literally.
		b.WriteByte('{')
		i++
	}
	return b.String()
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if i == 0 {
			if !isIdentStart(c) {
				return false
			}
		} else if !isIdentCont(c) {
			return false
		}
	}
	return true
}

func isIdentStart(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentCont(c rune) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
