package web

import (
	"unicode/utf8"

	"go.starlark.net/syntax"
)

// configRefs scans the raw Starlark for top-level node assignments
// (`id = plugin(...)`) and, for every keyword argument whose value expression
// references a variable (identifier) rather than being a pure literal, records a
// "reference-aware" rendering of that value keyed by node id → config key.
//
// The evaluated config (config.ParseBytes) resolves `env()` and module-level
// variables to their literal values, so the visual editor would otherwise
// re-inline a resolved secret when it re-serialises a node it only moved. By
// returning the original expression shape — identifiers rendered as the editor's
// {"__star_raw__": name} marker, which round-trips verbatim — the editor can
// preserve `api_key=TMDB_API_KEY`, `config=SMTP_CONFIG`, `list=[TRAKT_MOVIES_LIST]`
// and the like across a visual-editor save.
//
// Only reference-bearing kwargs are included; pure literals keep the resolved
// value from the evaluated config (more reliable for multi-line strings). The
// `upstream` kwarg is skipped — it is a graph edge, not config.
func configRefs(content string) map[string]map[string]any {
	out := map[string]map[string]any{}
	f, err := (&syntax.FileOptions{}).Parse("config", []byte(content), 0)
	if err != nil {
		return out
	}
	lineOffs := lineByteOffsets(content)
	for _, stmt := range f.Stmts {
		as, ok := stmt.(*syntax.AssignStmt)
		if !ok || as.Op != syntax.EQ {
			continue
		}
		lhs, ok := as.LHS.(*syntax.Ident)
		if !ok {
			continue
		}
		call, ok := as.RHS.(*syntax.CallExpr)
		if !ok {
			continue
		}
		for _, arg := range call.Args {
			be, ok := arg.(*syntax.BinaryExpr)
			if !ok || be.Op != syntax.EQ {
				continue
			}
			key, ok := be.X.(*syntax.Ident)
			if !ok || key.Name == "upstream" {
				continue
			}
			if !exprHasIdent(be.Y) {
				continue
			}
			if out[lhs.Name] == nil {
				out[lhs.Name] = map[string]any{}
			}
			out[lhs.Name][key.Name] = exprToRefValue(be.Y, content, lineOffs)
		}
	}
	return out
}

// exprHasIdent reports whether an expression references any variable — an
// identifier other than the True/False/None keywords.
func exprHasIdent(e syntax.Expr) bool {
	found := false
	syntax.Walk(e, func(n syntax.Node) bool {
		if id, ok := n.(*syntax.Ident); ok {
			switch id.Name {
			case "True", "False", "None":
			default:
				found = true
			}
		}
		return !found // stop descending once a reference is found
	})
	return found
}

// exprToRefValue renders an expression as a JSON-able value in which variable
// references become {"__star_raw__": name} (the visual editor's raw-expression
// marker) while literals keep their value. Containers recurse. Anything the
// walker doesn't model structurally (a call, a binary/unary expression) is
// preserved as its verbatim source text under __star_raw__.
func exprToRefValue(e syntax.Expr, content string, lineOffs []int) any {
	switch x := e.(type) {
	case *syntax.Literal:
		return x.Value
	case *syntax.Ident:
		switch x.Name {
		case "True":
			return true
		case "False":
			return false
		case "None":
			return nil
		}
		return map[string]any{"__star_raw__": x.Name}
	case *syntax.ListExpr:
		arr := make([]any, 0, len(x.List))
		for _, el := range x.List {
			arr = append(arr, exprToRefValue(el, content, lineOffs))
		}
		return arr
	case *syntax.TupleExpr:
		arr := make([]any, 0, len(x.List))
		for _, el := range x.List {
			arr = append(arr, exprToRefValue(el, content, lineOffs))
		}
		return arr
	case *syntax.DictExpr:
		m := map[string]any{}
		for _, entry := range x.List {
			de, ok := entry.(*syntax.DictEntry)
			if !ok {
				continue
			}
			k, ok := dictKeyString(de.Key)
			if !ok {
				// A non-string key can't round-trip through the editor's config
				// model; fall back to the verbatim source for the whole dict.
				return map[string]any{"__star_raw__": sliceSpan(content, lineOffs, e)}
			}
			m[k] = exprToRefValue(de.Value, content, lineOffs)
		}
		return m
	default:
		return map[string]any{"__star_raw__": sliceSpan(content, lineOffs, e)}
	}
}

// dictKeyString returns the string value of a dict key literal.
func dictKeyString(e syntax.Expr) (string, bool) {
	lit, ok := e.(*syntax.Literal)
	if !ok {
		return "", false
	}
	s, ok := lit.Value.(string)
	return s, ok
}

// lineByteOffsets returns the byte offset at which each 1-based line begins.
func lineByteOffsets(content string) []int {
	offs := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			offs = append(offs, i+1)
		}
	}
	return offs
}

// posByteOffset converts a Starlark (1-based line, 1-based rune column) position
// to a byte offset in content.
func posByteOffset(content string, lineOffs []int, p syntax.Position) int {
	if p.Line < 1 || int(p.Line) > len(lineOffs) {
		return -1
	}
	b := lineOffs[p.Line-1]
	for r := int32(1); r < p.Col; r++ {
		if b >= len(content) {
			break
		}
		_, size := utf8.DecodeRuneInString(content[b:])
		b += size
	}
	return b
}

// sliceSpan returns the verbatim source text covered by an expression's span.
func sliceSpan(content string, lineOffs []int, e syntax.Expr) string {
	start, end := e.Span()
	sb := posByteOffset(content, lineOffs, start)
	eb := posByteOffset(content, lineOffs, end)
	if sb < 0 || eb < 0 || sb > eb || eb > len(content) {
		return ""
	}
	return content[sb:eb]
}
