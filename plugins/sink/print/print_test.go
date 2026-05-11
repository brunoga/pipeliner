package print

import (
	"context"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test"}
}

func TestNewPrintPlugin(t *testing.T) {
	p, err := newPrintPlugin(map[string]any{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "print" {
		t.Errorf("want name %q, got %q", "print", p.Name())
	}
	if p.Phase() != plugin.PhaseOutput {
		t.Errorf("want phase output, got %s", p.Phase())
	}
}

func TestNewPrintPluginCustomFormat(t *testing.T) {
	p, err := newPrintPlugin(map[string]any{"format": "{{.Title}}"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pp := p.(*printPlugin)
	result, err := pp.ip.Render(map[string]any{"Title": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello" {
		t.Errorf("want %q, got %q", "hello", result)
	}
}

func TestNewPrintPluginInvalidTemplate(t *testing.T) {
	_, err := newPrintPlugin(map[string]any{"format": "{{.Bad"}, nil)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestOutputRunsWithoutError(t *testing.T) {
	p, _ := newPrintPlugin(map[string]any{}, nil)
	entries := []*entry.Entry{
		entry.New("title1", "http://a.com"),
		entry.New("title2", "http://b.com"),
	}
	if err := p.(plugin.SinkPlugin).Consume(context.Background(), makeCtx(), entries); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefaultFormatRendersFields(t *testing.T) {
	p, _ := newPrintPlugin(map[string]any{}, nil)
	pp := p.(*printPlugin)
	result, err := pp.ip.Render(map[string]any{"title": "my title", "url": "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "my title") {
		t.Errorf("expected title in output, got %q", result)
	}
}

func TestRegistered(t *testing.T) {
	d, ok := plugin.Lookup("print")
	if !ok {
		t.Fatal("print plugin not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("want role sink, got %s", d.Role)
	}
}

