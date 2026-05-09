package clog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func makeFilteredLogger(buf *bytes.Buffer, pluginNames ...string) *slog.Logger {
	inner := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewPluginFilter(inner, pluginNames))
}

// TestPluginFilterPassesMatchingPlugin verifies that records whose "plugin"
// attribute matches the filter target are written to the output.
func TestPluginFilterPassesMatchingPlugin(t *testing.T) {
	var buf bytes.Buffer
	log := makeFilteredLogger(&buf, "metainfo_tvdb")
	log.With("task", "t", "phase", "metainfo", "plugin", "metainfo_tvdb").
		Info("search cache hit", "series", "Breaking Bad")

	if buf.Len() == 0 {
		t.Error("expected log output for matching plugin, got none")
	}
	if got := buf.String(); !strings.Contains(got, "Breaking Bad") {
		t.Errorf("expected message in output, got: %s", got)
	}
}

// TestPluginFilterDropsNonMatchingPlugin verifies that records whose "plugin"
// attribute does not match the filter target are suppressed.
func TestPluginFilterDropsNonMatchingPlugin(t *testing.T) {
	var buf bytes.Buffer
	log := makeFilteredLogger(&buf, "metainfo_tvdb")
	log.With("task", "t", "phase", "filter", "plugin", "series").
		Info("no show match", "series", "Breaking Bad")

	if buf.Len() != 0 {
		t.Errorf("expected no output for non-matching plugin, got: %s", buf.String())
	}
}

// TestPluginFilterPassesTaskLevelLogs verifies that records with no "plugin"
// attribute (task-level logs) always pass through.
func TestPluginFilterPassesTaskLevelLogs(t *testing.T) {
	var buf bytes.Buffer
	log := makeFilteredLogger(&buf, "metainfo_tvdb")
	log.With("task", "t").Info("task started")

	if buf.Len() == 0 {
		t.Error("expected task-level log to pass through, got none")
	}
}

// TestPluginFilterWithAttrsInheritance verifies that a child logger created
// with a different plugin via With() is filtered independently.
func TestPluginFilterWithAttrsInheritance(t *testing.T) {
	var buf bytes.Buffer
	base := makeFilteredLogger(&buf, "metainfo_tvdb")

	// This child should be suppressed.
	base.With("plugin", "series").Info("filtered out")
	if buf.Len() != 0 {
		t.Errorf("child with non-matching plugin should be suppressed, got: %s", buf.String())
	}

	// This child should pass through.
	base.With("plugin", "metainfo_tvdb").Info("should appear")
	if buf.Len() == 0 {
		t.Error("child with matching plugin should appear")
	}
}

// TestPluginFilterMultiplePlugins verifies that records for any of the named
// plugins are passed through, and records for unlisted plugins are suppressed.
func TestPluginFilterMultiplePlugins(t *testing.T) {
	var buf bytes.Buffer
	log := makeFilteredLogger(&buf, "metainfo_tvdb", "metainfo_tmdb")

	log.With("plugin", "metainfo_tvdb").Info("tvdb hit")
	if !strings.Contains(buf.String(), "tvdb hit") {
		t.Error("metainfo_tvdb should pass through")
	}

	buf.Reset()
	log.With("plugin", "metainfo_tmdb").Info("tmdb hit")
	if !strings.Contains(buf.String(), "tmdb hit") {
		t.Error("metainfo_tmdb should pass through")
	}

	buf.Reset()
	log.With("plugin", "series").Info("series record")
	if buf.Len() != 0 {
		t.Errorf("unlisted plugin should be suppressed, got: %s", buf.String())
	}
}

// TestPluginFilterTaskLevelAlwaysPassesWithMultiple verifies that task-level
// logs pass through regardless of how many plugins are in the filter set.
func TestPluginFilterTaskLevelAlwaysPassesWithMultiple(t *testing.T) {
	var buf bytes.Buffer
	log := makeFilteredLogger(&buf, "metainfo_tvdb", "metainfo_tmdb")
	log.With("task", "tv").Info("task started")
	if buf.Len() == 0 {
		t.Error("task-level log should pass through with multi-plugin filter")
	}
}

// TestPluginFilterDebugLevel verifies that the underlying level filtering
// still applies — DEBUG records require --log-level debug.
func TestPluginFilterDebugLevel(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	log := slog.New(NewPluginFilter(inner, []string{"metainfo_tvdb"}))

	log.With("plugin", "metainfo_tvdb").Debug("should be suppressed by level")
	if buf.Len() != 0 {
		t.Errorf("DEBUG record should be suppressed at INFO level, got: %s", buf.String())
	}
}

