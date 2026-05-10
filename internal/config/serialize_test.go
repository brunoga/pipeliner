package config

import (
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/task"
)

func TestConfigToStarlarkEmpty(t *testing.T) {
	c := &Config{Tasks: map[string][]task.PluginConfig{}, Schedules: map[string]string{}}
	if got := ConfigToStarlark(c); got != "\n" {
		t.Errorf("empty config: got %q", got)
	}
}

func TestConfigToStarlarkSinglePlugin(t *testing.T) {
	c := &Config{
		Tasks: map[string][]task.PluginConfig{
			"tv": {{Name: "rss", Config: map[string]any{"url": "https://example.com"}}},
		},
		Schedules: map[string]string{"tv": "1h"},
	}
	out := ConfigToStarlark(c)
	if !strings.Contains(out, `task("tv"`) { t.Error("missing task call") }
	if !strings.Contains(out, `plugin("rss"`) { t.Error("missing plugin call") }
	if !strings.Contains(out, `url=`) { t.Error("missing url kwarg") }
	if !strings.Contains(out, `schedule="1h"`) { t.Error("missing schedule") }
}

func TestConfigToStarlarkBoolAndList(t *testing.T) {
	c := &Config{
		Tasks: map[string][]task.PluginConfig{
			"t": {{Name: "series", Config: map[string]any{
				"static":   []any{"Breaking Bad"},
				"tracking": "follow",
			}}},
		},
	}
	out := ConfigToStarlark(c)
	if !strings.Contains(out, `"Breaking Bad"`) { t.Error("missing list item") }
	if !strings.Contains(out, `"follow"`) { t.Error("missing tracking value") }
}

func TestConfigToStarlarkRoundTrip(t *testing.T) {
	// Serialize then parse back — Tasks must match.
	original := &Config{
		Tasks: map[string][]task.PluginConfig{
			"tv": {
				{Name: "rss",  Config: map[string]any{"url": "https://feeds.example.com/tv"}},
				{Name: "seen", Config: map[string]any{}},
				{Name: "series", Config: map[string]any{"static": []any{"Breaking Bad"}, "tracking": "follow"}},
			},
		},
		Schedules: map[string]string{"tv": "1h"},
	}
	star := ConfigToStarlark(original)
	roundTripped, err := ParseBytes([]byte(star))
	if err != nil {
		t.Fatalf("round-trip parse failed: %v\nGenerated Starlark:\n%s", err, star)
	}
	tvPlugins := roundTripped.Tasks["tv"]
	if len(tvPlugins) != 3 {
		t.Errorf("want 3 plugins after round-trip, got %d", len(tvPlugins))
	}
	if tvPlugins[0].Name != "rss" { t.Errorf("plugin[0]: got %q", tvPlugins[0].Name) }
	if tvPlugins[2].Config["tracking"] != "follow" { t.Errorf("tracking: got %v", tvPlugins[2].Config["tracking"]) }
	if roundTripped.Schedules["tv"] != "1h" { t.Errorf("schedule: got %q", roundTripped.Schedules["tv"]) }
}

func TestConfigToStarlarkMultipleTasks(t *testing.T) {
	c := &Config{
		Tasks: map[string][]task.PluginConfig{
			"b-task": {{Name: "seen", Config: map[string]any{}}},
			"a-task": {{Name: "rss",  Config: map[string]any{"url": "https://x.com"}}},
		},
	}
	out := ConfigToStarlark(c)
	// a-task should appear before b-task (alphabetical)
	aIdx := strings.Index(out, `"a-task"`)
	bIdx := strings.Index(out, `"b-task"`)
	if aIdx > bIdx { t.Error("tasks not in alphabetical order") }
}
