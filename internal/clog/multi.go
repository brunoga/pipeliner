package clog

import (
	"context"
	"log/slog"
)

// Multi returns a [slog.Handler] that dispatches each record to all handlers.
func Multi(handlers ...slog.Handler) slog.Handler {
	hs := make([]slog.Handler, len(handlers))
	copy(hs, handlers)
	return &multiHandler{hs: hs}
}

type multiHandler struct {
	hs []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m.hs {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var last error
	for _, h := range m.hs {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				last = err
			}
		}
	}
	return last
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		hs[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{hs: hs}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		hs[i] = h.WithGroup(name)
	}
	return &multiHandler{hs: hs}
}
