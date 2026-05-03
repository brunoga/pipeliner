// Package expr evaluates boolean infix expressions against a data map.
//
// Expressions like:
//
//	tmdb_vote_average > 7.0
//	source == "CAM"
//	tmdb_vote_average >= 6.5 and source != "CAM"
//	not (source == "CAM" or source == "TS")
//
// Supported operators (binary): ==, !=, <, <=, >, >=, contains, matches
// Supported logical: and (&&), or (||), not (!)
// Parentheses are supported for grouping.
//
// Strings containing "{{" are treated as Go templates for backward compat.
package expr

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	itpl "github.com/brunoga/pipeliner/internal/template"
)

// Expr is a compiled boolean expression.
type Expr struct {
	node node
	// tmpl is set when the expression uses Go template syntax (backward compat).
	tmpl *template.Template
}

// Compile parses an expression string and returns an Expr.
// Strings containing "{{" are compiled as Go templates.
func Compile(s string) (*Expr, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "{{") {
		tmpl, err := template.New("cond").Funcs(itpl.FuncMap()).Parse(s)
		if err != nil {
			return nil, fmt.Errorf("expr: invalid template: %w", err)
		}
		return &Expr{tmpl: tmpl}, nil
	}
	p := &parser{lex: newLexer(s)}
	n, err := p.parseOr()
	if err != nil {
		return nil, fmt.Errorf("expr: %w", err)
	}
	if p.lex.peek().kind != tokEOF {
		return nil, fmt.Errorf("expr: unexpected token %q after expression", p.lex.peek().val)
	}
	return &Expr{node: n}, nil
}

// Eval evaluates the expression against data, returning true or false.
func (e *Expr) Eval(data map[string]any) (bool, error) {
	if e.tmpl != nil {
		var buf bytes.Buffer
		if err := e.tmpl.Execute(&buf, data); err != nil {
			return false, fmt.Errorf("expr: template execute: %w", err)
		}
		s := strings.TrimSpace(buf.String())
		return truthy(s), nil
	}
	v, err := e.node.eval(data)
	if err != nil {
		return false, err
	}
	switch vv := v.(type) {
	case bool:
		return vv, nil
	case string:
		return truthy(vv), nil
	case float64:
		return vv != 0, nil
	default:
		return false, fmt.Errorf("expr: expression did not evaluate to a boolean")
	}
}

func truthy(s string) bool {
	if s == "" {
		return false
	}
	l := strings.ToLower(s)
	return l != "false" && l != "0"
}

// ---------------------------------------------------------------------------
// Lexer
// ---------------------------------------------------------------------------

type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokString
	tokNumber
	tokLParen
	tokRParen
	tokEq  // ==
	tokNeq // !=
	tokLt  // <
	tokLte // <=
	tokGt  // >
	tokGte // >=
	tokAnd // and / &&
	tokOr  // or / ||
	tokNot // not / !
)

type token struct {
	kind tokKind
	val  string
}

type lexer struct {
	src    string
	pos    int
	peeked *token
}

func newLexer(src string) *lexer { return &lexer{src: src} }

func (l *lexer) peek() token {
	if l.peeked == nil {
		t := l.next()
		l.peeked = &t
	}
	return *l.peeked
}

func (l *lexer) next() token {
	if l.peeked != nil {
		t := *l.peeked
		l.peeked = nil
		return t
	}
	return l.scan()
}

func (l *lexer) scan() token {
	// Skip whitespace.
	for l.pos < len(l.src) && isSpace(l.src[l.pos]) {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{kind: tokEOF}
	}
	c := l.src[l.pos]

	switch {
	case c == '(':
		l.pos++
		return token{kind: tokLParen, val: "("}
	case c == ')':
		l.pos++
		return token{kind: tokRParen, val: ")"}
	case c == '!' && l.peek2() == '=':
		l.pos += 2
		return token{kind: tokNeq, val: "!="}
	case c == '!':
		l.pos++
		return token{kind: tokNot, val: "!"}
	case c == '<' && l.peek2() == '=':
		l.pos += 2
		return token{kind: tokLte, val: "<="}
	case c == '<':
		l.pos++
		return token{kind: tokLt, val: "<"}
	case c == '>' && l.peek2() == '=':
		l.pos += 2
		return token{kind: tokGte, val: ">="}
	case c == '>':
		l.pos++
		return token{kind: tokGt, val: ">"}
	case c == '=' && l.peek2() == '=':
		l.pos += 2
		return token{kind: tokEq, val: "=="}
	case c == '&' && l.peek2() == '&':
		l.pos += 2
		return token{kind: tokAnd, val: "&&"}
	case c == '|' && l.peek2() == '|':
		l.pos += 2
		return token{kind: tokOr, val: "||"}
	case c == '"' || c == '\'':
		return l.scanString(c)
	case isDigit(c) || (c == '-' && l.pos+1 < len(l.src) && isDigit(l.src[l.pos+1])):
		return l.scanNumber()
	case isIdentStart(c):
		return l.scanIdent()
	}
	// Unknown character — return it as a single-char ident so parser can report error.
	l.pos++
	return token{kind: tokIdent, val: string(c)}
}

func (l *lexer) peek2() byte {
	if l.pos+1 < len(l.src) {
		return l.src[l.pos+1]
	}
	return 0
}

func (l *lexer) scanString(quote byte) token {
	l.pos++ // skip opening quote
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == quote {
			l.pos++
			return token{kind: tokString, val: b.String()}
		}
		if c == '\\' && l.pos+1 < len(l.src) {
			l.pos++
			switch l.src[l.pos] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(l.src[l.pos])
			}
			l.pos++
			continue
		}
		b.WriteByte(c)
		l.pos++
	}
	// Unterminated string — return what we have.
	return token{kind: tokString, val: b.String()}
}

func (l *lexer) scanNumber() token {
	start := l.pos
	if l.src[l.pos] == '-' {
		l.pos++
	}
	for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
		l.pos++
	}
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		l.pos++
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.pos++
		}
	}
	return token{kind: tokNumber, val: l.src[start:l.pos]}
}

func (l *lexer) scanIdent() token {
	start := l.pos
	for l.pos < len(l.src) && isIdentCont(l.src[l.pos]) {
		l.pos++
	}
	word := l.src[start:l.pos]
	switch strings.ToLower(word) {
	case "and":
		return token{kind: tokAnd, val: word}
	case "or":
		return token{kind: tokOr, val: word}
	case "not":
		return token{kind: tokNot, val: word}
	case "contains":
		// Infix "contains" handled as a binary op in the parser.
		return token{kind: tokIdent, val: word}
	case "matches":
		return token{kind: tokIdent, val: word}
	default:
		return token{kind: tokIdent, val: word}
	}
}

func isSpace(c byte) bool      { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' }
func isIdentCont(c byte) bool  { return isIdentStart(c) || isDigit(c) }

// ---------------------------------------------------------------------------
// AST nodes
// ---------------------------------------------------------------------------

type node interface {
	eval(data map[string]any) (any, error)
}

type boolNode struct{ v bool }
type stringNode struct{ v string }
type numberNode struct{ v float64 }
type identNode struct{ name string }

type binaryNode struct {
	op    string
	left  node
	right node
}

type unaryNode struct {
	op  string
	arg node
}

// funcNode represents a built-in function call.
type funcNode struct {
	name string
	arg  node // nil for no-arg functions
}

func (n *boolNode) eval(_ map[string]any) (any, error)   { return n.v, nil }
func (n *stringNode) eval(_ map[string]any) (any, error) { return n.v, nil }
func (n *numberNode) eval(_ map[string]any) (any, error) { return n.v, nil }

func (n *identNode) eval(data map[string]any) (any, error) {
	if v, ok := data[n.name]; ok {
		return v, nil
	}
	// Unknown field → empty string (lenient).
	return "", nil
}

func (n *funcNode) eval(data map[string]any) (any, error) {
	switch strings.ToLower(n.name) {
	case "now":
		return time.Now(), nil

	case "daysago":
		if n.arg == nil {
			return nil, fmt.Errorf("daysago() requires one argument")
		}
		v, err := n.arg.eval(data)
		if err != nil {
			return nil, err
		}
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("daysago(): argument must be numeric")
		}
		return time.Now().AddDate(0, 0, -int(f)), nil

	case "weeksago":
		if n.arg == nil {
			return nil, fmt.Errorf("weeksago() requires one argument")
		}
		v, err := n.arg.eval(data)
		if err != nil {
			return nil, err
		}
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("weeksago(): argument must be numeric")
		}
		return time.Now().AddDate(0, 0, -7*int(f)), nil

	case "monthsago":
		if n.arg == nil {
			return nil, fmt.Errorf("monthsago() requires one argument")
		}
		v, err := n.arg.eval(data)
		if err != nil {
			return nil, err
		}
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("monthsago(): argument must be numeric")
		}
		return time.Now().AddDate(0, -int(f), 0), nil

	case "date":
		if n.arg == nil {
			return nil, fmt.Errorf("date() requires one argument")
		}
		v, err := n.arg.eval(data)
		if err != nil {
			return nil, err
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("date(): argument must be a string")
		}
		for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05Z07:00", time.RFC3339} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, nil
			}
		}
		return nil, fmt.Errorf("date(): cannot parse %q as a date", s)

	default:
		return nil, fmt.Errorf("unknown function %q", n.name)
	}
}

func (n *unaryNode) eval(data map[string]any) (any, error) {
	v, err := n.arg.eval(data)
	if err != nil {
		return nil, err
	}
	b, err := asBool(v)
	if err != nil {
		return nil, fmt.Errorf("not: %w", err)
	}
	return !b, nil
}

func (n *binaryNode) eval(data map[string]any) (any, error) {
	switch n.op {
	case "and", "&&":
		l, err := n.left.eval(data)
		if err != nil {
			return nil, err
		}
		lb, err := asBool(l)
		if err != nil {
			return nil, fmt.Errorf("and: left: %w", err)
		}
		if !lb {
			return false, nil // short-circuit
		}
		r, err := n.right.eval(data)
		if err != nil {
			return nil, err
		}
		rb, err := asBool(r)
		if err != nil {
			return nil, fmt.Errorf("and: right: %w", err)
		}
		return rb, nil

	case "or", "||":
		l, err := n.left.eval(data)
		if err != nil {
			return nil, err
		}
		lb, err := asBool(l)
		if err != nil {
			return nil, fmt.Errorf("or: left: %w", err)
		}
		if lb {
			return true, nil // short-circuit
		}
		r, err := n.right.eval(data)
		if err != nil {
			return nil, err
		}
		rb, err := asBool(r)
		if err != nil {
			return nil, fmt.Errorf("or: right: %w", err)
		}
		return rb, nil
	}

	l, err := n.left.eval(data)
	if err != nil {
		return nil, err
	}
	r, err := n.right.eval(data)
	if err != nil {
		return nil, err
	}

	switch n.op {
	case "contains":
		ls, err := asString(l)
		if err != nil {
			return nil, fmt.Errorf("contains: left must be a string: %w", err)
		}
		rs, err := asString(r)
		if err != nil {
			return nil, fmt.Errorf("contains: right must be a string: %w", err)
		}
		return strings.Contains(ls, rs), nil

	case "matches":
		ls, err := asString(l)
		if err != nil {
			return nil, fmt.Errorf("matches: left must be a string: %w", err)
		}
		rs, err := asString(r)
		if err != nil {
			return nil, fmt.Errorf("matches: right must be a string: %w", err)
		}
		ok, err := regexp.MatchString(rs, ls)
		if err != nil {
			return nil, fmt.Errorf("matches: invalid regexp %q: %w", rs, err)
		}
		return ok, nil

	case "==":
		return compareEq(l, r)
	case "!=":
		eq, err := compareEq(l, r)
		return !eq, err
	case "<":
		cmp, err := compareNum(l, r, "<")
		return cmp, err
	case "<=":
		cmp, err := compareNum(l, r, "<=")
		return cmp, err
	case ">":
		cmp, err := compareNum(l, r, ">")
		return cmp, err
	case ">=":
		cmp, err := compareNum(l, r, ">=")
		return cmp, err
	}
	return nil, fmt.Errorf("unknown operator %q", n.op)
}

// toTime attempts to convert a value to time.Time. Returns (zero, false) if not possible.
func toTime(v any) (time.Time, bool) {
	switch vv := v.(type) {
	case time.Time:
		return vv, true
	case string:
		for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05Z"} {
			if t, err := time.Parse(layout, vv); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func compareEq(l, r any) (bool, error) {
	// Check for time.Time values first.
	lt, lok := toTime(l)
	rt, rok := toTime(r)
	if lok && rok {
		return lt.Equal(rt), nil
	}

	// Both numeric?
	lf, lok2 := toFloat(l)
	rf, rok2 := toFloat(r)
	if lok2 && rok2 {
		return math.Abs(lf-rf) < 1e-12, nil
	}
	// Both string (or coerce).
	ls, _ := asString(l)
	rs, _ := asString(r)
	return ls == rs, nil
}

func compareNum(l, r any, op string) (bool, error) {
	// Check for time.Time values first.
	lt, lok := toTime(l)
	rt, rok := toTime(r)
	if lok && rok {
		switch op {
		case "<":
			return lt.Before(rt), nil
		case "<=":
			return lt.Before(rt) || lt.Equal(rt), nil
		case ">":
			return lt.After(rt), nil
		case ">=":
			return lt.After(rt) || lt.Equal(rt), nil
		}
	}

	lf, lok2 := toFloat(l)
	rf, rok2 := toFloat(r)
	if !lok2 || !rok2 {
		// Fall back to string comparison.
		ls, _ := asString(l)
		rs, _ := asString(r)
		switch op {
		case "<":
			return ls < rs, nil
		case "<=":
			return ls <= rs, nil
		case ">":
			return ls > rs, nil
		case ">=":
			return ls >= rs, nil
		}
	}
	switch op {
	case "<":
		return lf < rf, nil
	case "<=":
		return lf <= rf, nil
	case ">":
		return lf > rf, nil
	case ">=":
		return lf >= rf, nil
	}
	return false, fmt.Errorf("unknown numeric op %q", op)
}

func toFloat(v any) (float64, bool) {
	switch vv := v.(type) {
	case float64:
		return vv, true
	case float32:
		return float64(vv), true
	case int:
		return float64(vv), true
	case int32:
		return float64(vv), true
	case int64:
		return float64(vv), true
	case string:
		f, err := strconv.ParseFloat(vv, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func asString(v any) (string, error) {
	switch vv := v.(type) {
	case string:
		return vv, nil
	case bool:
		if vv {
			return "true", nil
		}
		return "false", nil
	case float64:
		return strconv.FormatFloat(vv, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(vv), nil
	case int64:
		return strconv.FormatInt(vv, 10), nil
	case nil:
		return "", nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func asBool(v any) (bool, error) {
	switch vv := v.(type) {
	case bool:
		return vv, nil
	case string:
		return truthy(vv), nil
	case float64:
		return vv != 0, nil
	case int:
		return vv != 0, nil
	case int64:
		return vv != 0, nil
	case nil:
		return false, nil
	}
	return false, fmt.Errorf("cannot convert %T to bool", v)
}

// ---------------------------------------------------------------------------
// Recursive descent parser
// ---------------------------------------------------------------------------

type parser struct {
	lex *lexer
}

// parseOr: or_expr = and_expr ( ("or"|"||") and_expr )*
func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		t := p.lex.peek()
		if t.kind != tokOr {
			break
		}
		p.lex.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: t.val, left: left, right: right}
	}
	return left, nil
}

// parseAnd: and_expr = not_expr ( ("and"|"&&") not_expr )*
func (p *parser) parseAnd() (node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for {
		t := p.lex.peek()
		if t.kind != tokAnd {
			break
		}
		p.lex.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: t.val, left: left, right: right}
	}
	return left, nil
}

// parseNot: not_expr = ("not"|"!") not_expr | comparison
func (p *parser) parseNot() (node, error) {
	t := p.lex.peek()
	if t.kind == tokNot {
		p.lex.next()
		arg, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &unaryNode{op: t.val, arg: arg}, nil
	}
	return p.parseComparison()
}

// parseComparison: value ( op value )?
// where op includes "contains" and "matches" (as identifier tokens)
func (p *parser) parseComparison() (node, error) {
	left, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	t := p.lex.peek()
	switch t.kind {
	case tokEq, tokNeq, tokLt, tokLte, tokGt, tokGte:
		p.lex.next()
		right, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return &binaryNode{op: t.val, left: left, right: right}, nil
	case tokIdent:
		lower := strings.ToLower(t.val)
		if lower == "contains" || lower == "matches" {
			p.lex.next()
			right, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			return &binaryNode{op: lower, left: left, right: right}, nil
		}
	}
	return left, nil
}

// parseValue: literal | ident | func_call | "(" expr ")"
func (p *parser) parseValue() (node, error) {
	t := p.lex.next()
	switch t.kind {
	case tokLParen:
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.lex.peek().kind != tokRParen {
			return nil, fmt.Errorf("expected ')'")
		}
		p.lex.next()
		return inner, nil
	case tokString:
		return &stringNode{v: t.val}, nil
	case tokNumber:
		f, err := strconv.ParseFloat(t.val, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", t.val)
		}
		return &numberNode{v: f}, nil
	case tokIdent:
		switch strings.ToLower(t.val) {
		case "true":
			return &boolNode{v: true}, nil
		case "false":
			return &boolNode{v: false}, nil
		}
		// Check if this is a function call.
		if p.lex.peek().kind == tokLParen {
			return p.parseFuncCall(t.val)
		}
		return &identNode{name: t.val}, nil
	case tokEOF:
		return nil, fmt.Errorf("unexpected end of expression")
	}
	return nil, fmt.Errorf("unexpected token %q", t.val)
}

// parseFuncCall parses a function call: name "(" [arg] ")"
// The opening "(" has not yet been consumed.
func (p *parser) parseFuncCall(name string) (node, error) {
	p.lex.next() // consume '('

	// Check for zero-arg function.
	if p.lex.peek().kind == tokRParen {
		p.lex.next() // consume ')'
		return &funcNode{name: name}, nil
	}

	// Parse single argument.
	arg, err := p.parseOr()
	if err != nil {
		return nil, fmt.Errorf("function %q: %w", name, err)
	}
	if p.lex.peek().kind != tokRParen {
		return nil, fmt.Errorf("function %q: expected ')'", name)
	}
	p.lex.next() // consume ')'
	return &funcNode{name: name, arg: arg}, nil
}
