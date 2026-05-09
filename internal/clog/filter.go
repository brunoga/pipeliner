package clog

import (
	"context"
	"log/slog"
)

// pluginFilter is a slog.Handler that drops records whose accumulated "plugin"
// attribute is not in the configured set. Records with no "plugin" attribute
// (task-level logs) always pass through.
type pluginFilter struct {
	inner     slog.Handler
	targets   map[string]bool // plugin names to keep
	pluginVal string          // "plugin" attr value accumulated via WithAttrs ("" = not set)
}

// NewPluginFilter wraps inner so that only log records for the named plugins
// (and task-level records with no plugin attribute) are passed through.
// Pass multiple names to show output from several plugins simultaneously.
func NewPluginFilter(inner slog.Handler, pluginNames []string) slog.Handler {
	targets := make(map[string]bool, len(pluginNames))
	for _, name := range pluginNames {
		targets[name] = true
	}
	return &pluginFilter{inner: inner, targets: targets}
}

func (f *pluginFilter) Enabled(ctx context.Context, l slog.Level) bool {
	return f.inner.Enabled(ctx, l)
}

func (f *pluginFilter) Handle(ctx context.Context, r slog.Record) error {
	// If WithAttrs set a plugin attr, use it for filtering.
	if f.pluginVal != "" {
		if !f.targets[f.pluginVal] {
			return nil
		}
		return f.inner.Handle(ctx, r)
	}
	// Otherwise scan the record's own attrs.
	var recordPlugin string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "plugin" {
			recordPlugin = a.Value.String()
			return false
		}
		return true
	})
	// Records with no plugin attr (task-level) always pass through.
	if recordPlugin != "" && !f.targets[recordPlugin] {
		return nil
	}
	return f.inner.Handle(ctx, r)
}

func (f *pluginFilter) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &pluginFilter{
		inner:     f.inner.WithAttrs(attrs),
		targets:   f.targets,
		pluginVal: f.pluginVal,
	}
	for _, a := range attrs {
		if a.Key == "plugin" {
			next.pluginVal = a.Value.String()
		}
	}
	return next
}

func (f *pluginFilter) WithGroup(name string) slog.Handler {
	return &pluginFilter{
		inner:     f.inner.WithGroup(name),
		targets:   f.targets,
		pluginVal: f.pluginVal,
	}
}
