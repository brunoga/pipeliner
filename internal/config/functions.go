package config

// functions.go handles discovery and runtime tracking of user-defined pipeline
// functions — Starlark def blocks annotated with at least one # pipeliner:
// comment that the visual editor can surface as reusable palette entries.
//
// A user function looks like:
//
//	# Human-readable description.
//	# pipeliner:param quality  Minimum quality spec, e.g. "1080p+"
//	def quality_filter(upstream, quality="1080p"):
//	    s = process("seen",    upstream=upstream)
//	    q = process("quality", upstream=s, min=quality)
//	    return q
//
// Discovery is a two-phase process:
//  1. Pre-execution text scan (scanUserFunctions) — extracts names, params,
//     descriptions, and role from the source without running Starlark.
//  2. Runtime tagging — built-ins tag each dagNodeRecord with the enclosing
//     function call key so pipelineBuiltin can group them into records.

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// UserFunctionParam describes one parameter of a user-defined function.
type UserFunctionParam struct {
	Name      string
	Type      plugin.FieldType
	Required  bool
	Default   any    // nil if Required
	Hint      string
	Multiline bool
}

// UserFunctionDef holds the schema discovered for a user-defined function.
type UserFunctionDef struct {
	Name        string
	Role        string // "source" | "processor" | "sink"
	Description string
	Params      []UserFunctionParam
}

// FunctionCallRecord records one invocation of a user function inside a pipeline.
type FunctionCallRecord struct {
	CallKey         string         // "funcName@line:col" — unique per call site
	FuncName        string
	Args            map[string]any // kwargs passed at the call site
	InternalNodeIDs []string       // IDs of nodes created inside the function body
	ReturnNodeID    string         // node ID returned by the function
}

var (
	// defRe matches "def funcname(" at the start of a line.
	defRe = regexp.MustCompile(`^def\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`)
	// paramLineRe parses "# pipeliner:param name hint text".
	paramLineRe = regexp.MustCompile(`^pipeliner:param\s+(\S+)\s*(.*)$`)
)

// scanUserFunctions performs a text-only pre-scan of src and returns a map of
// all user functions that have at least one "# pipeliner:" comment immediately
// above their "def" line.
func scanUserFunctions(src string) map[string]*UserFunctionDef {
	result := make(map[string]*UserFunctionDef)

	lines := strings.Split(src, "\n")
	var commentLines []string  // non-pipeliner comment lines (description)
	var paramHints []struct{ name, hint string }

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		switch {
		case trimmed == "":
			commentLines = nil
			paramHints = nil

		case strings.HasPrefix(trimmed, "#"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			if m := paramLineRe.FindStringSubmatch(rest); m != nil {
				paramHints = append(paramHints, struct{ name, hint string }{m[1], strings.TrimSpace(m[2])})
			} else if strings.HasPrefix(rest, "pipeliner:") {
				// other pipeliner: metadata — counts as the opt-in marker but no text
				commentLines = append(commentLines, "") // preserve opt-in signal
			} else {
				commentLines = append(commentLines, rest)
			}

		default:
			if m := defRe.FindStringSubmatch(trimmed); m != nil {
				funcName := m[1]
				// Only expose functions that had at least one # pipeliner: comment.
				hasPipelinerComment := false
				for _, ph := range paramHints {
					_ = ph
					hasPipelinerComment = true
				}
				// Also check if any commentLine was added by a pipeliner: prefix.
				for _, cl := range commentLines {
					if cl == "" { // sentinel from pipeliner: lines
						hasPipelinerComment = true
					}
				}

				if hasPipelinerComment || len(paramHints) > 0 {
					// Collect the function body (indented lines after the def).
					body := collectFunctionBody(lines, i)

					// Build description from non-empty comment lines.
					var descParts []string
					for _, cl := range commentLines {
						if cl != "" {
							descParts = append(descParts, cl)
						}
					}

					// Parse the function signature for parameters.
					params := parseFunctionParams(trimmed, paramHints)

					result[funcName] = &UserFunctionDef{
						Name:        funcName,
						Role:        inferFunctionRole(body),
						Description: strings.Join(descParts, " "),
						Params:      params,
					}
				}
			}
			commentLines = nil
			paramHints = nil
		}
	}
	return result
}

// collectFunctionBody returns the lines that form the body of the def starting
// at defLine (lines after defLine that are indented or blank).
func collectFunctionBody(lines []string, defLine int) string {
	var body strings.Builder
	for i := defLine + 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" || len(line) == 0 {
			body.WriteByte('\n')
			continue
		}
		// Body ends at the first non-empty, non-indented line.
		if line[0] != ' ' && line[0] != '\t' {
			break
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	return body.String()
}

// inferFunctionRole returns "source", "processor", or "sink" by scanning the
// function body for input() or output() calls.
func inferFunctionRole(body string) string {
	hasInput  := strings.Contains(body, "input(")
	hasOutput := strings.Contains(body, "output(")
	if hasInput {
		return "source"
	}
	if hasOutput {
		return "sink"
	}
	return "processor"
}

// parseFunctionParams parses the def signature line to extract parameter names
// and defaults, then cross-references with paramHints for hint text.
//
// The "upstream" parameter (or any parameter whose default is a node handle)
// is the wiring argument and is excluded from the config schema.
func parseFunctionParams(defLine string, hints []struct{ name, hint string }) []UserFunctionParam {
	// Extract the part between the first "(" and the matching ")".
	open := strings.Index(defLine, "(")
	if open < 0 {
		return nil
	}
	// Walk to find the closing ")" accounting for nesting.
	depth := 0
	close := -1
	for j := open; j < len(defLine); j++ {
		switch defLine[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = j
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		close = len(defLine)
	}
	sig := defLine[open+1 : close]

	hintMap := make(map[string]string, len(hints))
	for _, h := range hints {
		hintMap[h.name] = h.hint
	}

	var params []UserFunctionParam
	for _, part := range splitParams(sig) {
		part = strings.TrimSpace(part)
		if part == "" || part == "*" || part == "**" {
			continue
		}
		// Strip type annotations if any (we don't use them).
		if idx := strings.Index(part, ":"); idx >= 0 {
			part = strings.TrimSpace(part[:idx])
		}

		name, rawDefault, hasDefault := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		rawDefault = strings.TrimSpace(rawDefault)

		// Skip the wiring parameter.
		if name == "upstream" || name == "*args" || name == "**kwargs" {
			continue
		}

		p := UserFunctionParam{
			Name:     name,
			Hint:     hintMap[name],
			Required: !hasDefault,
		}

		if hasDefault {
			p.Type, p.Default = inferTypeAndDefault(rawDefault)
		} else {
			p.Type = plugin.FieldTypeString
		}
		params = append(params, p)
	}
	return params
}

// splitParams splits a comma-separated parameter list, respecting nested
// brackets so defaults like '["a", "b"]' are kept intact.
func splitParams(sig string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(sig); i++ {
		switch sig[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, sig[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, sig[start:])
	return parts
}

// inferTypeAndDefault infers a FieldType and a Go default value from a raw
// Starlark default expression string (e.g. `"1080p"`, `5`, `True`, `["a"]`).
func inferTypeAndDefault(raw string) (plugin.FieldType, any) {
	raw = strings.TrimSpace(raw)

	// Boolean
	if raw == "True" {
		return plugin.FieldTypeBool, true
	}
	if raw == "False" {
		return plugin.FieldTypeBool, false
	}
	// Integer
	if n, err := strconv.Atoi(raw); err == nil {
		return plugin.FieldTypeInt, n
	}
	// Float coerced to int if possible (rare in practice)
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return plugin.FieldTypeInt, int(f)
	}
	// List literal starting with "["
	if strings.HasPrefix(raw, "[") {
		return plugin.FieldTypeList, parseStringList(raw)
	}
	// String literal (single or double quoted)
	if len(raw) >= 2 &&
		((raw[0] == '"' && raw[len(raw)-1] == '"') ||
			(raw[0] == '\'' && raw[len(raw)-1] == '\'')) {
		return plugin.FieldTypeString, raw[1 : len(raw)-1]
	}
	// Fallback — treat as a string with the raw value as default.
	return plugin.FieldTypeString, raw
}

// parseStringList tries to extract string elements from a simple Starlark list
// literal like '["a", "b"]'. Returns nil on parse failure.
func parseStringList(raw string) []any {
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return []any{}
	}
	var out []any
	for _, part := range strings.Split(inner, ",") {
		s := strings.TrimSpace(part)
		if len(s) >= 2 &&
			((s[0] == '"' && s[len(s)-1] == '"') ||
				(s[0] == '\'' && s[len(s)-1] == '\'')) {
			out = append(out, s[1:len(s)-1])
		} else {
			out = append(out, s)
		}
	}
	return out
}
