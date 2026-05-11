package set

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func process(t *testing.T, p plugin.Plugin, e *entry.Entry) {
	t.Helper()
	if _, err := p.(plugin.ProcessorPlugin).Process(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("Process error: %v", err)
	}
}

func TestSetLiteralField(t *testing.T) {
	p, err := newSetPlugin(map[string]any{"category": "tv"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("Show S01E01", "http://x.com")
	process(t, p, e)
	if got := e.GetString("category"); got != "tv" {
		t.Errorf("want %q, got %q", "tv", got)
	}
}

func TestSetTemplateField(t *testing.T) {
	p, err := newSetPlugin(map[string]any{"label": "{{.Title}}-tagged"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("MyShow", "http://x.com")
	process(t, p, e)
	if got := e.GetString("label"); got != "MyShow-tagged" {
		t.Errorf("want %q, got %q", "MyShow-tagged", got)
	}
}

func TestSetFromExistingField(t *testing.T) {
	p, err := newSetPlugin(map[string]any{"copy": "{{.src}}"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("t", "u")
	e.Set("src", "original-value")
	process(t, p, e)
	if got := e.GetString("copy"); got != "original-value" {
		t.Errorf("want %q, got %q", "original-value", got)
	}
}

func TestSetMultipleFields(t *testing.T) {
	p, err := newSetPlugin(map[string]any{
		"a": "alpha",
		"b": "beta",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("t", "u")
	process(t, p, e)
	if e.GetString("a") != "alpha" || e.GetString("b") != "beta" {
		t.Errorf("multi-field set failed: a=%q b=%q", e.GetString("a"), e.GetString("b"))
	}
}

func TestSetInvalidTemplate(t *testing.T) {
	_, err := newSetPlugin(map[string]any{"bad": "{{.Unclosed"}, nil)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestRegistered(t *testing.T) {
	d, ok := plugin.Lookup("set")
	if !ok {
		t.Fatal("set plugin not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("want role processor, got %s", d.Role)
	}
}
