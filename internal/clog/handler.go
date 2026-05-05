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

// renderMsg writes the message to buf with appropriate ANSI coloring.
// Most messages get a single color prefix; a few use inline mixed colors.
func renderMsg(buf *bytes.Buffer, msg string, l slog.Level, color bool) {
	if !color {
		buf.WriteString(msg)
		return
	}
	switch msg {
	case "entry accepted then rejected":
		buf.WriteString(ansiRed + ansiBold + "entry " + ansiGreen + "accepted" + ansiRed + " then rejected" + ansiReset)
		return
	case "entry accepted then failed":
		buf.WriteString(ansiRed + ansiBold + "entry " + ansiGreen + "accepted" + ansiRed + " then failed" + ansiReset)
		return
	}
	mc := msgColor(msg, l)
	if mc != "" {
		buf.WriteString(mc)
	}
	buf.WriteString(msg)
	if mc != "" {
		buf.WriteString(ansiReset)
	}
}

// msgColor returns an ANSI prefix for the log message.
// Entry state messages use distinct colors; other messages inherit the level color.
func msgColor(msg string, l slog.Level) string {
	switch msg {
	case "entry accepted":
		return ansiGreen + ansiBold
	case "entry rejected":
		return ansiRed + ansiBold
	case "entry undecided":
		return ansiYellow
	case "entry failed":
		return ansiMagenta + ansiBold
	}
	switch {
	case l >= slog.LevelError:
		return ansiRed
	case l >= slog.LevelWarn:
		return ansiYellow
	case l >= slog.LevelInfo:
		return ansiCyan
	}
	return ""
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
