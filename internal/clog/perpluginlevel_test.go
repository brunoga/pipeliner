package clog

import (
	"context"
	"log/slog"
	"slices"
	"testing"
)

// recordingHandler captures every Record passed to Handle so tests can assert
// on emit decisions. Honors a level threshold like a real handler.
type recordingHandler struct {
	level   slog.Level
	records []slog.Record
}

func (h *recordingHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func newPPL(level slog.Level) (*recordingHandler, *PerPluginLevel) {
	rec := &recordingHandler{level: level}
	return rec, NewPerPluginLevel(rec)
}

// TestPerPluginLevel_EmptySetIsPassthrough confirms the wrapper degrades to
// the inner handler's behaviour when no overrides are registered — Debug is
// still dropped at an INFO-level inner, Info still passes.
func TestPerPluginLevel_EmptySetIsPassthrough(t *testing.T) {
	rec, ppl := newPPL(slog.LevelInfo)
	log := slog.New(ppl)

	log.Debug("dropped", "plugin", "metainfo_bluray")
	log.Info("passes", "plugin", "metainfo_bluray")

	if len(rec.records) != 1 {
		t.Fatalf("got %d records, want 1", len(rec.records))
	}
	if rec.records[0].Message != "passes" {
		t.Errorf("forwarded record: got %q, want %q", rec.records[0].Message, "passes")
	}
}

// TestPerPluginLevel_RegisteredPluginGetsDebug confirms the core behaviour:
// after SetDebugPlugins enables a plugin, that plugin's Debug records pass
// through while other plugins' Debug records continue to be dropped.
func TestPerPluginLevel_RegisteredPluginGetsDebug(t *testing.T) {
	rec, ppl := newPPL(slog.LevelInfo)
	ppl.SetDebugPlugins([]string{"metainfo_bluray"})
	log := slog.New(ppl)

	log.Debug("bluray trace", "plugin", "metainfo_bluray", "key", "avatar")
	log.Debug("tvdb trace", "plugin", "metainfo_tvdb", "key", "breaking-bad")
	log.Info("regular info", "plugin", "metainfo_tvdb")

	if len(rec.records) != 2 {
		t.Fatalf("got %d records, want 2 (bluray-debug + tvdb-info)", len(rec.records))
	}
	msgs := []string{rec.records[0].Message, rec.records[1].Message}
	slices.Sort(msgs)
	want := []string{"bluray trace", "regular info"}
	if !slices.Equal(msgs, want) {
		t.Errorf("forwarded messages: got %v, want %v", msgs, want)
	}
}

// TestPerPluginLevel_NoPluginAttrAtDebugIsDropped: an unrelated Debug record
// (no plugin attribute) must NOT bypass the inner threshold just because some
// other plugin has debug enabled.
func TestPerPluginLevel_NoPluginAttrAtDebugIsDropped(t *testing.T) {
	rec, ppl := newPPL(slog.LevelInfo)
	ppl.SetDebugPlugins([]string{"metainfo_bluray"})
	log := slog.New(ppl)

	log.Debug("no plugin attr — should not pass")

	if len(rec.records) != 0 {
		t.Fatalf("debug record with no plugin attr leaked through: %v", rec.records)
	}
}

// TestPerPluginLevel_PluginAttrViaWithAttrs confirms that a child logger
// created with .With("plugin", "X") carries the plugin identity through to
// the override check, even though the Record itself doesn't repeat the attr.
// This is the path the executor takes via tc.Logger.With(..., "plugin", name).
func TestPerPluginLevel_PluginAttrViaWithAttrs(t *testing.T) {
	rec, ppl := newPPL(slog.LevelInfo)
	ppl.SetDebugPlugins([]string{"metainfo_bluray"})
	root := slog.New(ppl)

	child := root.With("plugin", "metainfo_bluray", "node", "metainfo_bluray_42")
	child.Debug("by-id resolve", "id", "26954")

	if len(rec.records) != 1 {
		t.Fatalf("With-derived child did not forward debug: got %d records", len(rec.records))
	}
	if rec.records[0].Message != "by-id resolve" {
		t.Errorf("forwarded record: got %q, want %q", rec.records[0].Message, "by-id resolve")
	}
}

// TestPerPluginLevel_SetAfterDerivationIsObserved is the key correctness check
// for the shared-pointer design: a child logger created BEFORE SetDebugPlugins
// runs must observe the update without being re-derived. If WithAttrs copied
// the atomic by value instead of sharing it, this test would fail.
func TestPerPluginLevel_SetAfterDerivationIsObserved(t *testing.T) {
	rec, ppl := newPPL(slog.LevelInfo)
	root := slog.New(ppl)
	child := root.With("plugin", "metainfo_bluray")

	// First emit while no overrides set — must be dropped.
	child.Debug("first", "id", "x")
	if len(rec.records) != 0 {
		t.Fatalf("debug leaked before override set: %v", rec.records)
	}

	// Now flip on the override and emit again from the SAME child logger.
	ppl.SetDebugPlugins([]string{"metainfo_bluray"})
	child.Debug("second", "id", "y")

	if len(rec.records) != 1 {
		t.Fatalf("override not observed by pre-existing child logger; got %d records", len(rec.records))
	}
	if rec.records[0].Message != "second" {
		t.Errorf("forwarded record: got %q, want %q", rec.records[0].Message, "second")
	}
}

// TestPerPluginLevel_DebugPluginsRoundTrip confirms SetDebugPlugins and
// DebugPlugins are consistent and that DebugPlugins returns a deduped, sorted
// snapshot.
func TestPerPluginLevel_DebugPluginsRoundTrip(t *testing.T) {
	_, ppl := newPPL(slog.LevelInfo)

	if got := ppl.DebugPlugins(); got != nil {
		t.Errorf("initial DebugPlugins: got %v, want nil", got)
	}

	ppl.SetDebugPlugins([]string{"metainfo_tvdb", "metainfo_bluray", "", "metainfo_bluray"})
	got := ppl.DebugPlugins()
	want := []string{"metainfo_bluray", "metainfo_tvdb"} // sorted, deduped, empty dropped
	if !slices.Equal(got, want) {
		t.Errorf("DebugPlugins: got %v, want %v", got, want)
	}

	ppl.SetDebugPlugins(nil)
	if got := ppl.DebugPlugins(); got != nil {
		t.Errorf("after clear: got %v, want nil", got)
	}
}

// TestPerPluginLevel_InnerThresholdRecordsAlwaysPass confirms that records at
// or above the inner handler's level pass through regardless of plugin
// overrides. The override system must never DROP something — only ADD.
func TestPerPluginLevel_InnerThresholdRecordsAlwaysPass(t *testing.T) {
	rec, ppl := newPPL(slog.LevelWarn) // inner gates Info too
	ppl.SetDebugPlugins([]string{"metainfo_bluray"})
	log := slog.New(ppl)

	log.Error("error from any plugin", "plugin", "unrelated")
	log.Warn("warning", "plugin", "unrelated")
	log.Info("info — gated by inner", "plugin", "metainfo_bluray")

	if len(rec.records) != 2 {
		t.Fatalf("forwarded %d records, want 2 (error+warn pass, info gated by inner)", len(rec.records))
	}
}
