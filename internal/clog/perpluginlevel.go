package clog

import (
	"context"
	"log/slog"
	"sort"
	"sync/atomic"
)

// PerPluginLevel is a slog.Handler wrapper that lets a named subset of plugins
// emit DEBUG records while the rest of the system follows the inner handler's
// configured level. The intended use is runtime debug toggling from the web UI:
// the user picks the one plugin they want to trace, and only that plugin's
// debug output appears — the rest of the logs stay at INFO.
//
// The override set is held in an atomic pointer so SetDebugPlugins can be
// called from one goroutine (the HTTP handler) while Handle is running on many
// others without locking. Swap is O(1); reads are a single atomic load.
//
// WithAttrs/WithGroup derivatives share the same atomic pointer by reference,
// so a single SetDebugPlugins call on the root handler immediately affects
// every child logger spawned via slog.With.
type PerPluginLevel struct {
	inner        slog.Handler
	debugPlugins *atomic.Pointer[map[string]struct{}] // shared across With* derivatives
	pluginVal    string                               // accumulated via WithAttrs ("" = not yet set)
}

// NewPerPluginLevel wraps inner. With an empty override set the wrapper is a
// pass-through; callers can construct it unconditionally and flip plugins on
// at runtime via SetDebugPlugins.
func NewPerPluginLevel(inner slog.Handler) *PerPluginLevel {
	ptr := &atomic.Pointer[map[string]struct{}]{}
	empty := map[string]struct{}{}
	ptr.Store(&empty)
	return &PerPluginLevel{inner: inner, debugPlugins: ptr}
}

// SetDebugPlugins replaces the override set. Pass an empty slice (or nil) to
// disable per-plugin debug entirely. Names are stored as-is — they must match
// the plugin's registered Name() exactly.
func (h *PerPluginLevel) SetDebugPlugins(names []string) {
	next := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n != "" {
			next[n] = struct{}{}
		}
	}
	h.debugPlugins.Store(&next)
}

// DebugPlugins returns the current override set as a sorted slice. Safe to call
// while SetDebugPlugins runs concurrently.
func (h *PerPluginLevel) DebugPlugins() []string {
	m := h.debugPlugins.Load()
	if m == nil || len(*m) == 0 {
		return nil
	}
	out := make([]string, 0, len(*m))
	for name := range *m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (h *PerPluginLevel) Enabled(ctx context.Context, level slog.Level) bool {
	if h.inner.Enabled(ctx, level) {
		return true
	}
	// The inner handler would drop this record on level alone. But if any
	// per-plugin override is active, a downstream Debug record might still need
	// to come through — return true so the logger constructs the Record and
	// hands it to Handle, where we can check the plugin attribute.
	if level == slog.LevelDebug {
		m := h.debugPlugins.Load()
		if m != nil && len(*m) > 0 {
			return true
		}
	}
	return false
}

func (h *PerPluginLevel) Handle(ctx context.Context, r slog.Record) error {
	// Records that already meet the inner threshold pass through unconditionally.
	if h.inner.Enabled(ctx, r.Level) {
		return h.inner.Handle(ctx, r)
	}
	// Below threshold: only forward when (a) it's a Debug record and (b) the
	// record's plugin attribute matches one of the active overrides.
	if r.Level != slog.LevelDebug {
		return nil
	}
	m := h.debugPlugins.Load()
	if m == nil || len(*m) == 0 {
		return nil
	}
	plugin := h.pluginVal
	if plugin == "" {
		// Fall back to scanning the record's own attrs.
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "plugin" {
				plugin = a.Value.String()
				return false
			}
			return true
		})
	}
	if plugin == "" {
		return nil
	}
	if _, ok := (*m)[plugin]; !ok {
		return nil
	}
	return h.inner.Handle(ctx, r)
}

func (h *PerPluginLevel) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &PerPluginLevel{
		inner:        h.inner.WithAttrs(attrs),
		debugPlugins: h.debugPlugins, // shared pointer — see type doc
		pluginVal:    h.pluginVal,
	}
	for _, a := range attrs {
		if a.Key == "plugin" {
			next.pluginVal = a.Value.String()
		}
	}
	return next
}

func (h *PerPluginLevel) WithGroup(name string) slog.Handler {
	return &PerPluginLevel{
		inner:        h.inner.WithGroup(name),
		debugPlugins: h.debugPlugins,
		pluginVal:    h.pluginVal,
	}
}
