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
// Each pair becomes one list item.
func td(pairs ...string) TaskDef {
	var d TaskDef
	for i := 0; i+1 < len(pairs); i += 2 {
		d = append(d, taskEntry{pairs[i], json.RawMessage(pairs[i+1])})
	}
	return d
}

// --- tests ---

func TestLoadValidConfig(t *testing.T) {
	path := writeTempConfig(t, `
tasks:
  my-task:
    - nop-input: {}
    - nop-output: {}
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

func TestVariablesSubstitution(t *testing.T) {
	path := writeTempConfig(t, `
variables:
  feed_url: "http://example.com/rss"
  db_path: ":memory:"
tasks:
  t:
    - nop-input:
        url: "{$ feed_url $}"
    - nop-output:
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
	raw := c.Tasks["t"][0].raw
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
    - nop-input:
        x: "{$ unknown $}"
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := c.Tasks["t"][0].raw
	if !strings.Contains(string(raw), "{$ unknown $}") {
		t.Errorf("unknown variable should be left unchanged, got: %s", raw)
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
    - nop-input:
        url: "${PIPELINER_TEST_URL}"
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	raw := c.Tasks["t"][0].raw
	if !strings.Contains(string(raw), "http://example.com/rss") {
		t.Errorf("env var not substituted: %s", raw)
	}
}

func TestEnvVarMissingReturnsError(t *testing.T) {
	path := writeTempConfig(t, `
tasks:
  t:
    - nop-input:
        x: "${PIPELINER_TEST_DEFINITELY_NOT_SET_XYZ}"
`)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing env var")
	}
}

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

// --- use: expansion tests ---

// TestUseNoParams tests the scalar use: "template_name" shorthand with a
// zero-parameter template.
func TestUseNoParams(t *testing.T) {
	path := writeTempConfig(t, `
templates:
  common:
    nop-input: {}
    nop-output: {}
tasks:
  t:
    - use: common
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tasks, err := BuildTasks(c, nil, nil)
	if err != nil {
		t.Fatalf("BuildTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("want 1 task, got %d", len(tasks))
	}
}

// TestUsePositionalParams tests that [template, arg1, arg2] correctly maps
// args to params and performs {$ param $} substitution.
func TestUsePositionalParams(t *testing.T) {
	path := writeTempConfig(t, `
templates:
  common-output:
    params: [dest, host]
    nop-input:
      url: "{$ dest $}"
    nop-output-validated:
      host: "{$ host $}"
tasks:
  t:
    - use: [common-output, "/media/tv", "localhost"]
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Validate that the expanded config passes validation (host is provided).
	if errs := Validate(c); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}

	// Check substitution in the expanded task.
	expanded, err := expandTaskDef("t", c.Tasks["t"], c.Templates)
	if err != nil {
		t.Fatalf("expandTaskDef: %v", err)
	}
	var nopInputRaw, nopOutputRaw json.RawMessage
	for _, e := range expanded {
		switch e.name {
		case "nop-input":
			nopInputRaw = e.raw
		case "nop-output-validated":
			nopOutputRaw = e.raw
		}
	}
	if !strings.Contains(string(nopInputRaw), "/media/tv") {
		t.Errorf("dest not substituted in nop-input: %s", nopInputRaw)
	}
	if !strings.Contains(string(nopOutputRaw), "localhost") {
		t.Errorf("host not substituted in nop-output-validated: %s", nopOutputRaw)
	}
}

// TestUseMissingParams tests that passing too few args is a validation error.
func TestUseMissingParams(t *testing.T) {
	c := &Config{
		Templates: map[string]TemplateDef{
			"tmpl": {
				taskEntry{"params", json.RawMessage(`["dest","host"]`)},
				taskEntry{"nop-input", json.RawMessage(`{"url":"{$ dest $}"}`)},
			},
		},
		Tasks: map[string]TaskDef{
			"t": {taskEntry{"use", json.RawMessage(`["tmpl","/media/tv"]`)}},
		},
	}
	_, err := expandTaskDef("t", c.Tasks["t"], c.Templates)
	if err == nil {
		t.Error("expected error for too few args")
	}
}

// TestUseTooManyParams tests that passing too many args is a validation error.
func TestUseTooManyParams(t *testing.T) {
	c := &Config{
		Templates: map[string]TemplateDef{
			"tmpl": {
				taskEntry{"params", json.RawMessage(`["dest"]`)},
				taskEntry{"nop-input", json.RawMessage(`{"url":"{$ dest $}"}`)},
			},
		},
		Tasks: map[string]TaskDef{
			"t": {taskEntry{"use", json.RawMessage(`["tmpl","/media/tv","extra"]`)}},
		},
	}
	_, err := expandTaskDef("t", c.Tasks["t"], c.Templates)
	if err == nil {
		t.Error("expected error for too many args")
	}
}

// TestUseMultipleExpansions tests that two use: entries at different positions
// both expand correctly.
func TestUseMultipleExpansions(t *testing.T) {
	path := writeTempConfig(t, `
templates:
  input-tmpl:
    params: [url]
    nop-input:
      url: "{$ url $}"
  output-tmpl:
    nop-output: {}
tasks:
  t:
    - use: [input-tmpl, "https://example.com/rss"]
    - use: output-tmpl
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	expanded, err := expandTaskDef("t", c.Tasks["t"], c.Templates)
	if err != nil {
		t.Fatalf("expandTaskDef: %v", err)
	}
	if len(expanded) != 2 {
		t.Fatalf("want 2 entries after expansion, got %d", len(expanded))
	}
	if expanded[0].name != "nop-input" {
		t.Errorf("entry[0]: got %q, want nop-input", expanded[0].name)
	}
	if expanded[1].name != "nop-output" {
		t.Errorf("entry[1]: got %q, want nop-output", expanded[1].name)
	}
}

// TestUsePreservesOrder verifies that plugins appear in the correct order:
// pre-use entries, then template expansion, then post-use entries.
func TestUsePreservesOrder(t *testing.T) {
	path := writeTempConfig(t, `
templates:
  middle:
    nop-output: {}
tasks:
  t:
    - nop-input: {}
    - use: middle
    - priority: 3
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	expanded, err := expandTaskDef("t", c.Tasks["t"], c.Templates)
	if err != nil {
		t.Fatalf("expandTaskDef: %v", err)
	}
	// Expect: nop-input, nop-output, priority
	want := []string{"nop-input", "nop-output", "priority"}
	if len(expanded) != len(want) {
		t.Fatalf("want %d entries, got %d: %v", len(want), len(expanded), expanded)
	}
	for i, name := range want {
		if expanded[i].name != name {
			t.Errorf("entry[%d]: got %q, want %q", i, expanded[i].name, name)
		}
	}
}

// TestUseUnknownTemplate tests that referencing a non-existent template is an error.
func TestUseUnknownTemplate(t *testing.T) {
	c := &Config{
		Templates: map[string]TemplateDef{},
		Tasks: map[string]TaskDef{
			"t": {taskEntry{"use", json.RawMessage(`"no-such-template"`)}},
		},
	}
	_, err := expandTaskDef("t", c.Tasks["t"], c.Templates)
	if err == nil {
		t.Error("expected error for unknown template")
	}
}

// TestUseValidateRunsOnExpanded ensures that per-plugin validators see the
// fully expanded config after template substitution.
func TestUseValidateRunsOnExpanded(t *testing.T) {
	// Template provides the required "host" field via param substitution.
	path := writeTempConfig(t, `
templates:
  with-host:
    params: [host]
    nop-input: {}
    nop-output-validated:
      host: "{$ host $}"
tasks:
  t:
    - use: [with-host, "myhost"]
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if errs := Validate(c); len(errs) != 0 {
		t.Errorf("expected no errors when required field comes from template param, got: %v", errs)
	}
}
