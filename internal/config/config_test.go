package config

import (
	"fmt"
	"os"
	"testing"

	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/task"

	_ "github.com/brunoga/pipeliner/plugins/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/input/rss"
	_ "github.com/brunoga/pipeliner/plugins/modify/pathfmt"
)

func parseOK(t *testing.T, src string) *Config {
	t.Helper()
	c, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	return c
}

func parseFail(t *testing.T, src string) error {
	t.Helper()
	_, err := ParseBytes([]byte(src))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	return err
}

func newDB(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ---- parse ----

func TestParseEmpty(t *testing.T) {
	c := parseOK(t, ``)
	if len(c.Tasks) != 0 {
		t.Errorf("want 0 tasks, got %d", len(c.Tasks))
	}
}

func TestParseBasicTask(t *testing.T) {
	c := parseOK(t, `
task("tv", [
    plugin("rss", url="https://example.com/feed"),
    plugin("seen"),
])
`)
	plugins := c.Tasks["tv"]
	if len(plugins) != 2 {
		t.Fatalf("want 2 plugins, got %d", len(plugins))
	}
	if plugins[0].Name != "rss" {
		t.Errorf("plugins[0]: got %q", plugins[0].Name)
	}
	if plugins[0].Config["url"] != "https://example.com/feed" {
		t.Errorf("rss url: got %v", plugins[0].Config["url"])
	}
}

func TestParseSchedule(t *testing.T) {
	c := parseOK(t, `task("tv", [plugin("seen")], schedule="1h")`)
	if s := c.Schedules["tv"]; s != "1h" {
		t.Errorf("schedule: got %q, want 1h", s)
	}
}

func TestParseMultipleTasks(t *testing.T) {
	c := parseOK(t, `
task("a", [plugin("seen")])
task("b", [plugin("seen")], schedule="6h")
`)
	if len(c.Tasks) != 2 {
		t.Errorf("want 2 tasks, got %d", len(c.Tasks))
	}
	if c.Schedules["b"] != "6h" {
		t.Errorf("schedule b: got %q", c.Schedules["b"])
	}
}

func TestParsePluginDictForm(t *testing.T) {
	c := parseOK(t, `
task("t", [plugin("pathfmt", {"path": "/data/{title}", "field": "dl"})])
`)
	cfg := c.Tasks["t"][0].Config
	if cfg["path"] != "/data/{title}" {
		t.Errorf("path: got %v", cfg["path"])
	}
}

func TestParsePluginNestedList(t *testing.T) {
	c := parseOK(t, `task("t", [plugin("seen", fields=["url", "title"])])`)
	fields, ok := c.Tasks["t"][0].Config["fields"].([]any)
	if !ok || len(fields) != 2 {
		t.Errorf("fields: got %v (%T)", c.Tasks["t"][0].Config["fields"], c.Tasks["t"][0].Config["fields"])
	}
}

func TestParseVariablesAreJustVariables(t *testing.T) {
	c := parseOK(t, `
base = "https://example.com"
task("t", [plugin("rss", url=base + "/feed")])
`)
	if url := c.Tasks["t"][0].Config["url"]; url != "https://example.com/feed" {
		t.Errorf("url: got %v", url)
	}
}

func TestParseTemplateFunction(t *testing.T) {
	c := parseOK(t, `
def common():
    return [plugin("rss", url="https://x.com"), plugin("seen")]

task("t", common())
`)
	if len(c.Tasks["t"]) != 2 {
		t.Errorf("want 2 plugins from template, got %d", len(c.Tasks["t"]))
	}
}

func TestParseTemplateFunctionWithParams(t *testing.T) {
	c := parseOK(t, `
def feed(url):
    return [plugin("rss", url=url)]

task("t", feed("https://example.com/rss") + [plugin("seen")])
`)
	if len(c.Tasks["t"]) != 2 {
		t.Fatalf("want 2 plugins, got %d", len(c.Tasks["t"]))
	}
	if c.Tasks["t"][0].Config["url"] != "https://example.com/rss" {
		t.Error("url mismatch")
	}
}

func TestParseEnvPresent(t *testing.T) {
	t.Setenv("TEST_CFG_URL", "https://env.example.com")
	c := parseOK(t, `task("t", [plugin("rss", url=env("TEST_CFG_URL"))])`)
	if c.Tasks["t"][0].Config["url"] != "https://env.example.com" {
		t.Errorf("env: got %v", c.Tasks["t"][0].Config["url"])
	}
}

func TestParseEnvDefault(t *testing.T) {
	os.Unsetenv("PIPELINER_NOTSET_XYZZY") //nolint:errcheck
	c := parseOK(t, `task("t", [plugin("rss", url=env("PIPELINER_NOTSET_XYZZY", default="fallback"))])`)
	if c.Tasks["t"][0].Config["url"] != "fallback" {
		t.Errorf("default: got %v", c.Tasks["t"][0].Config["url"])
	}
}

func TestParseEnvMissingErrors(t *testing.T) {
	os.Unsetenv("PIPELINER_NOTSET_XYZZY") //nolint:errcheck
	parseFail(t, `env("PIPELINER_NOTSET_XYZZY")`)
}

func TestParseSyntaxError(t *testing.T) {
	parseFail(t, `def broken(`)
}

func TestParseNonPluginInList(t *testing.T) {
	parseFail(t, `task("t", ["not a plugin"])`)
}

func TestParseLoad(t *testing.T) {
	dir := t.TempDir()
	helperPath := dir + "/common.star"
	if err := os.WriteFile(helperPath, []byte(`
def make_rss(url):
    return plugin("rss", url=url)
`), 0o600); err != nil {
		t.Fatal(err)
	}
	mainSrc := fmt.Sprintf(`
load(%q, "make_rss")
task("t", [make_rss("https://example.com/rss")])
`, helperPath)
	c, err := execute(dir+"/config.star", []byte(mainSrc))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if c.Tasks["t"][0].Config["url"] != "https://example.com/rss" {
		t.Errorf("loaded helper not applied")
	}
}

// ---- validate ----

func TestValidateKnownPlugin(t *testing.T) {
	c := parseOK(t, `task("t", [plugin("seen")])`)
	if errs := Validate(c); len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestValidateUnknownPlugin(t *testing.T) {
	c := parseOK(t, `task("t", [plugin("no_such_plugin_xyz")])`)
	if errs := Validate(c); len(errs) == 0 {
		t.Error("expected error for unknown plugin")
	}
}

func TestValidateFromPluginRejected(t *testing.T) {
	// PhaseFrom plugins must not appear as top-level task plugins.
	// Find any registered PhaseFrom plugin.
	names := []string{"jackett", "rss_search", "trakt_list", "tvdb_favorites"}
	for _, n := range names {
		d, ok := plugin.Lookup(n)
		if !ok || d.PluginPhase != plugin.PhaseFrom {
			continue
		}
		c := &Config{Tasks: map[string][]task.PluginConfig{
			"t": {{Name: n, Config: map[string]any{}}},
		}}
		if errs := Validate(c); len(errs) == 0 {
			t.Errorf("expected error for PhaseFrom plugin %q at top level", n)
		}
		return
	}
	t.Skip("no PhaseFrom plugin registered to test")
}

func TestValidatePluginConfigError(t *testing.T) {
	// rss requires a url key
	c := parseOK(t, `task("t", [plugin("rss")])`)
	if errs := Validate(c); len(errs) == 0 {
		t.Error("expected validation error for missing rss url")
	}
}

// ---- build ----

func TestBuildTasksBasic(t *testing.T) {
	c := parseOK(t, `
task("tv", [
    plugin("rss", url="https://example.com/feed"),
    plugin("seen"),
])
`)
	tasks, err := BuildTasks(c, newDB(t), nil)
	if err != nil {
		t.Fatalf("BuildTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name() != "tv" {
		t.Errorf("unexpected tasks: %v", tasks)
	}
}

func TestBuildTasksAlphabeticOrder(t *testing.T) {
	c := parseOK(t, `
task("z-task", [plugin("seen")])
task("a-task", [plugin("seen")])
task("m-task", [plugin("seen")])
`)
	tasks, err := BuildTasks(c, newDB(t), nil)
	if err != nil {
		t.Fatalf("BuildTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d", len(tasks))
	}
	if tasks[0].Name() != "a-task" || tasks[1].Name() != "m-task" || tasks[2].Name() != "z-task" {
		t.Errorf("order: %s %s %s", tasks[0].Name(), tasks[1].Name(), tasks[2].Name())
	}
}

func TestBuildTasksUnknownPlugin(t *testing.T) {
	c := &Config{
		Tasks: map[string][]task.PluginConfig{
			"t": {{Name: "no_such_plugin_xyz", Config: map[string]any{}}},
		},
	}
	if _, err := BuildTasks(c, newDB(t), nil); err == nil {
		t.Error("expected error for unknown plugin")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.star"
	if err := os.WriteFile(path, []byte(`task("t", [plugin("seen")], schedule="30m")`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Schedules["t"] != "30m" {
		t.Errorf("schedule: got %q", c.Schedules["t"])
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/tmp/does_not_exist_xyzzy.star"); err == nil {
		t.Error("expected error for missing file")
	}
}
