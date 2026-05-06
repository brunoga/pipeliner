package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// --- test plugin registration ---

type nopInput struct{}

func (n *nopInput) Name() string        { return "nop-input" }
func (n *nopInput) Phase() plugin.Phase { return plugin.PhaseInput }
func (n *nopInput) Run(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	return nil, nil
}

type nopOutput struct{}

func (n *nopOutput) Name() string        { return "nop-output" }
func (n *nopOutput) Phase() plugin.Phase { return plugin.PhaseOutput }
func (n *nopOutput) Output(_ context.Context, _ *plugin.TaskContext, _ []*entry.Entry) error {
	return nil
}

// nopOutputValidated is an output plugin that requires "host" in its config,
// used to test that Validate runs against the merged (post-template) config.
type nopOutputValidated struct{}

func (n *nopOutputValidated) Name() string        { return "nop-output-validated" }
func (n *nopOutputValidated) Phase() plugin.Phase { return plugin.PhaseOutput }
func (n *nopOutputValidated) Output(_ context.Context, _ *plugin.TaskContext, _ []*entry.Entry) error {
	return nil
}

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "nop-input",
		PluginPhase: plugin.PhaseInput,
		Factory:     func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) { return &nopInput{}, nil },
	})
	plugin.Register(&plugin.Descriptor{
		PluginName:  "nop-output",
		PluginPhase: plugin.PhaseOutput,
		Factory:     func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) { return &nopOutput{}, nil },
	})
	plugin.Register(&plugin.Descriptor{
		PluginName:  "nop-output-validated",
		PluginPhase: plugin.PhaseOutput,
		Factory:     func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) { return &nopOutputValidated{}, nil },
		Validate: func(cfg map[string]any) []error {
			if _, ok := cfg["host"].(string); !ok {
				return []error{fmt.Errorf("nop-output-validated: \"host\" is required")}
			}
			return nil
		},
	})
}

// --- helpers ---

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

// td builds a TaskDef from alternating name/raw-JSON string pairs.
func td(pairs ...string) TaskDef {
	var d TaskDef
	for i := 0; i+1 < len(pairs); i += 2 {
		d.set(pairs[i], json.RawMessage(pairs[i+1]))
	}
	return d
}

// --- tests ---

func TestLoadValidConfig(t *testing.T) {
	path := writeTempConfig(t, `
tasks:
  my-task:
    nop-input: {}
    nop-output: {}
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(c.Tasks) != 1 {
		t.Errorf("want 1 task, got %d", len(c.Tasks))
	}
	if _, ok := c.Tasks["my-task"]; !ok {
		t.Error("expected my-task in tasks")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	// A top-level sequence cannot be unmarshaled into the Config struct.
	path := writeTempConfig(t, `
- this is a list not a mapping
`)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for malformed config (list at top level)")
	}
}

func TestValidateKnownPlugins(t *testing.T) {
	c := &Config{
		Tasks: map[string]TaskDef{
			"t": td("nop-input", `{}`, "nop-output", `{}`),
		},
	}
	errs := Validate(c)
	if len(errs) != 0 {
		t.Errorf("expected no errors for known plugins, got: %v", errs)
	}
}

func TestValidateUnknownPlugin(t *testing.T) {
	c := &Config{
		Tasks: map[string]TaskDef{
			"t": td("no-such-plugin", `{}`),
		},
	}
	errs := Validate(c)
	if len(errs) == 0 {
		t.Error("expected validation error for unknown plugin")
	}
}

func TestBuildTasksSuccess(t *testing.T) {
	c := &Config{
		Tasks: map[string]TaskDef{
			"t": td("nop-input", `{}`, "nop-output", `{}`),
		},
	}
	tasks, err := BuildTasks(c, nil, nil)
	if err != nil {
		t.Fatalf("BuildTasks error: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("want 1 task, got %d", len(tasks))
	}
}

func TestBuildTasksUnknownPlugin(t *testing.T) {
	c := &Config{
		Tasks: map[string]TaskDef{
			"t": td("ghost", `{}`),
		},
	}
	_, err := BuildTasks(c, nil, nil)
	if err == nil {
		t.Error("expected error building task with unknown plugin")
	}
}

func TestTemplatesMerge(t *testing.T) {
	path := writeTempConfig(t, `
templates:
  base:
    nop-output: {}
tasks:
  t:
    template: base
    nop-input: {}
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := BuildTasks(c, nil, nil)
	if err != nil {
		t.Fatalf("BuildTasks error: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("want 1 task, got %d", len(tasks))
	}
}

func TestVariablesSubstitution(t *testing.T) {
	path := writeTempConfig(t, `
variables:
  feed_url: "http://example.com/rss"
  db_path: ":memory:"
tasks:
  t:
    nop-input:
      url: "{$ feed_url $}"
    nop-output:
      db: "{$ db_path $}"
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Variables["feed_url"] != "http://example.com/rss" {
		t.Errorf("variable feed_url: got %q", c.Variables["feed_url"])
	}
	// Verify substitution happened in task plugin config.
	raw, _ := c.Tasks["t"].get("nop-input")
	if !strings.Contains(string(raw), "http://example.com/rss") {
		t.Errorf("substitution not applied in task config: %s", raw)
	}
}

func TestVariablesUnknownKeyLeftUnchanged(t *testing.T) {
	path := writeTempConfig(t, `
variables:
  known: value
tasks:
  t:
    nop-input:
      x: "{$ unknown $}"
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := c.Tasks["t"].get("nop-input")
	if !strings.Contains(string(raw), "{$ unknown $}") {
		t.Errorf("unknown variable should be left unchanged, got: %s", raw)
	}
}

func TestTemplatesMergeUnknown(t *testing.T) {
	c := &Config{
		Tasks: map[string]TaskDef{
			"t": td("template", `"nonexistent"`),
		},
		Templates: map[string]TaskDef{},
	}
	_, err := BuildTasks(c, nil, nil)
	if err == nil {
		t.Error("expected error for missing template reference")
	}
}

func TestPrioritySorting(t *testing.T) {
	// Three tasks with different priorities; expect ascending order.
	c := &Config{
		Tasks: map[string]TaskDef{
			"low":  td("priority", `10`, "nop-input", `{}`, "nop-output", `{}`),
			"high": td("priority", `1`, "nop-input", `{}`, "nop-output", `{}`),
			"mid":  td("priority", `5`, "nop-input", `{}`, "nop-output", `{}`),
		},
	}
	tasks, err := BuildTasks(c, nil, nil)
	if err != nil {
		t.Fatalf("BuildTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d", len(tasks))
	}
	names := []string{tasks[0].Name(), tasks[1].Name(), tasks[2].Name()}
	want := []string{"high", "mid", "low"}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("position %d: got %q, want %q", i, names[i], n)
		}
	}
}

func TestEnvVarSubstitution(t *testing.T) {
	t.Setenv("PIPELINER_TEST_URL", "http://example.com/rss")

	path := writeTempConfig(t, `
tasks:
  t:
    nop-input:
      url: "${PIPELINER_TEST_URL}"
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	raw, _ := c.Tasks["t"].get("nop-input")
	if !strings.Contains(string(raw), "http://example.com/rss") {
		t.Errorf("env var not substituted: %s", raw)
	}
}

func TestEnvVarMissingReturnsError(t *testing.T) {
	path := writeTempConfig(t, `
tasks:
  t:
    nop-input:
      x: "${PIPELINER_TEST_DEFINITELY_NOT_SET_XYZ}"
`)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing env var")
	}
}

func TestTemplateBangOverride(t *testing.T) {
	c := &Config{
		Templates: map[string]TaskDef{
			"base": td("nop-input", `{"url":"http://template.com"}`, "nop-output", `{}`),
		},
		Tasks: map[string]TaskDef{
			"t": td("template", `"base"`, "nop-input!", `{"url":"http://override.com"}`),
		},
	}
	merged, err := mergeTemplate("t", c.Tasks["t"], c.Templates)
	if err != nil {
		t.Fatalf("mergeTemplate: %v", err)
	}
	raw, _ := merged.get("nop-input")
	if !strings.Contains(string(raw), "override.com") {
		t.Errorf("bang override didn't win; got %s", raw)
	}
	if strings.Contains(string(raw), "template.com") {
		t.Errorf("template value leaked through; got %s", raw)
	}
}

func TestTemplateJSONObjectMerge(t *testing.T) {
	c := &Config{
		Templates: map[string]TaskDef{
			"base": td("nop-input", `{"url":"http://example.com","timeout":30}`, "nop-output", `{}`),
		},
		Tasks: map[string]TaskDef{
			"t": td("template", `"base"`, "nop-input", `{"timeout":60}`),
		},
	}
	merged, err := mergeTemplate("t", c.Tasks["t"], c.Templates)
	if err != nil {
		t.Fatalf("mergeTemplate: %v", err)
	}
	raw, _ := merged.get("nop-input")
	// Both url (from template) and timeout=60 (from task) should be present.
	if !strings.Contains(string(raw), "http://example.com") {
		t.Errorf("template field 'url' missing from merge: %s", raw)
	}
	if !strings.Contains(string(raw), "60") {
		t.Errorf("task field 'timeout' missing from merge: %s", raw)
	}
	if strings.Contains(string(raw), `"timeout":30`) {
		t.Errorf("template timeout should be overridden by task: %s", raw)
	}
}

// TestValidateRunsOnMergedConfig ensures that per-plugin validators see the
// fully merged config (template fields + task fields) rather than just the
// task-level fragment. Regression test for the bug where a plugin split across
// a template (smtp settings) and a task (subject/body) failed validation
// because the validator only saw the task fragment.
func TestValidateRunsOnMergedConfig(t *testing.T) {
	// Template provides the required "host" field; task provides an extra field.
	c := &Config{
		Templates: map[string]TaskDef{
			"base": td("nop-input", `{}`, "nop-output-validated", `{"host":"localhost"}`),
		},
		Tasks: map[string]TaskDef{
			"t": td("template", `"base"`, "nop-output-validated", `{"port":8080}`),
		},
	}
	if errs := Validate(c); len(errs) != 0 {
		t.Errorf("expected no errors when required field comes from template, got: %v", errs)
	}
}

// TestValidateReportsErrorWhenRequiredFieldMissing confirms that validation
// still catches a genuinely missing required field after merging.
func TestValidateReportsErrorWhenRequiredFieldMissing(t *testing.T) {
	c := &Config{
		Tasks: map[string]TaskDef{
			"t": td("nop-input", `{}`, "nop-output-validated", `{"port":8080}`),
		},
	}
	errs := Validate(c)
	if len(errs) == 0 {
		t.Error("expected validation error for missing required field, got none")
	}
}
