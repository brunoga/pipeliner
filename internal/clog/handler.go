// Package clog provides a colorizing [slog.Handler] for terminal output.
package clog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/mattn/go-isatty"
)

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiCyan    = "\033[36m"
	ansiMagenta = "\033[35m"
	ansiGray    = "\033[90m"
)

// Handler is a [slog.Handler] that writes color-highlighted log lines.
// Color is auto-detected: enabled only when w is a terminal and NO_COLOR is unset.
type Handler struct {
	mu           sync.Mutex
	w            io.Writer
	opts         slog.HandlerOptions
	color        bool
	preFormatted []byte // attrs pre-serialized by WithAttrs
	groupPrefix  string // nested group prefix, e.g. "outer.inner."
}

// NewColored returns a Handler that always produces ANSI-colored output
// regardless of whether w is a terminal. Useful for streaming to web clients
// that convert ANSI codes to HTML.
func NewColored(w io.Writer, opts *slog.HandlerOptions) *Handler {
	h := New(w, opts)
	h.color = true
	return h
}

// New returns a Handler that writes to w.
func New(w io.Writer, opts *slog.HandlerOptions) *Handler {
	h := &Handler{w: w}
	if opts != nil {
		h.opts = *opts
	}
	if _, noColor := os.LookupEnv("NO_COLOR"); !noColor {
		if f, ok := w.(*os.File); ok && isatty.IsTerminal(f.Fd()) {
			h.color = enableColor(f)
		}
	}
	return h
}

func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	min := slog.LevelInfo
	if h.opts.Level != nil {
		min = h.opts.Level.Level()
	}
	return l >= min
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	if !r.Time.IsZero() {
		if h.color {
			buf.WriteString(ansiGray)
		}
		buf.WriteString(r.Time.Format("2006-01-02 15:04:05.000"))
		if h.color {
			buf.WriteString(ansiReset)
		}
		buf.WriteByte(' ')
	}

	lc := ""
	if h.color {
		lc = levelColor(r.Level)
	}
	if lc != "" {
		buf.WriteString(lc)
	}
	fmt.Fprintf(&buf, "%-5s", r.Level.String())
	if lc != "" {
		buf.WriteString(ansiReset)
	}
	buf.WriteByte(' ')

	renderMsg(&buf, r.Message, r.Level, h.color)

	if len(h.preFormatted) > 0 {
		buf.WriteByte(' ')
		buf.Write(h.preFormatted)
	}

	r.Attrs(func(a slog.Attr) bool {
		buf.WriteByte(' ')
		writeAttr(&buf, a, h.groupPrefix)
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h2 := h.clone()
	var buf bytes.Buffer
	buf.Write(h2.preFormatted)
	for _, a := range attrs {
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		writeAttr(&buf, a, h2.groupPrefix)
	}
	h2.preFormatted = buf.Bytes()
	return h2
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := h.clone()
	h2.groupPrefix += name + "."
	return h2
}

func (h *Handler) clone() *Handler {
	return &Handler{
		w:            h.w,
		opts:         h.opts,
		color:        h.color,
		preFormatted: bytes.Clone(h.preFormatted),
		groupPrefix:  h.groupPrefix,
	}
}

func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return ansiRed + ansiBold
	case l >= slog.LevelWarn:
		return ansiYellow
	case l >= slog.LevelInfo:
		return ansiCyan
	default: // Debug
		return ansiGray
	}
}

// keywords that receive their own fixed color inside any message.
var msgKeywords = []struct {
	word  string
	color string
}{
	{"accepted",  ansiGreen + ansiBold},
	{"rejected",  ansiRed + ansiBold},
	{"undecided", ansiYellow},
	{"failed",    ansiMagenta + ansiBold},
}

// renderMsg writes the message to buf. The base color comes from the log level
// (cyan for INFO, yellow for WARN, red for ERROR). Within that base color,
// the words "accepted", "rejected", "undecided", and "failed" are highlighted
// with their own fixed colors so they stand out regardless of context.
func renderMsg(buf *bytes.Buffer, msg string, l slog.Level, color bool) {
	if !color {
		buf.WriteString(msg)
		return
	}

	base := levelColor(l)

	// Fast path: no keywords present.
	hasKeyword := false
	for _, kw := range msgKeywords {
		if strings.Contains(msg, kw.word) {
			hasKeyword = true
			break
		}
	}
	if !hasKeyword {
		buf.WriteString(base)
		buf.WriteString(msg)
		buf.WriteString(ansiReset)
		return
	}

	// Scan left-to-right, coloring each keyword occurrence inline.
	buf.WriteString(base)
	rem := msg
	for len(rem) > 0 {
		// Find the earliest keyword in the remaining string.
		best, bestKW := len(rem), ""
		bestColor := ""
		for _, kw := range msgKeywords {
			if i := strings.Index(rem, kw.word); i >= 0 && i < best {
				best = i
				bestKW = kw.word
				bestColor = kw.color
			}
		}
		// Write text before the keyword in base color.
		buf.WriteString(rem[:best])
		if bestKW == "" {
			break
		}
		// Write the keyword in its own color, then restore base.
		buf.WriteString(ansiReset + bestColor + bestKW + ansiReset + base)
		rem = rem[best+len(bestKW):]
	}
	buf.WriteString(ansiReset)
}

func writeAttr(buf *bytes.Buffer, a slog.Attr, prefix string) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		gattrs := a.Value.Group()
		if len(gattrs) == 0 {
			return
		}
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for i, ga := range gattrs {
			if i > 0 {
				buf.WriteByte(' ')
			}
			writeAttr(buf, ga, p)
		}
		return
	}
	buf.WriteString(prefix)
	buf.WriteString(a.Key)
	buf.WriteByte('=')
	writeValue(buf, a.Value)
}

func writeValue(buf *bytes.Buffer, v slog.Value) {
	switch v.Kind() {
	case slog.KindString:
		writeQuoted(buf, v.String())
	case slog.KindAny:
		writeQuoted(buf, fmt.Sprint(v.Any()))
	default:
		buf.WriteString(v.String())
	}
}

func writeQuoted(buf *bytes.Buffer, s string) {
	if s == "" {
		buf.WriteString(`""`)
		return
	}
	for _, r := range s {
		if unicode.IsSpace(r) || r == '"' || r == '=' || r == '\\' {
			buf.WriteString(strconv.Quote(s))
			return
		}
	}
	buf.WriteString(s)
}
