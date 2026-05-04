package pathfmt

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func TestLiteralPath(t *testing.T) {
	p, err := newPlugin(map[string]any{"path": "/downloads/tv"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("My Show S01E01", "http://x.com/a")
	if err := p.(*pathfmtPlugin).Modify(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	if v := e.GetString("download_path"); v != "/downloads/tv" {
		t.Errorf("download_path: got %q", v)
	}
}

func TestTemplateInterpolation(t *testing.T) {
	p, _ := newPlugin(map[string]any{"path": "/downloads/{{.series_name}}/Season {{.series_season}}"}, nil)
	e := entry.New("My Show S02E03", "http://x.com/a")
	e.Set("series_name", "My Show")
	e.Set("series_season", 2)
	p.(*pathfmtPlugin).Modify(context.Background(), makeCtx(), e) //nolint:errcheck
	if v := e.GetString("download_path"); v != "/downloads/My Show/Season 2" {
		t.Errorf("download_path: got %q", v)
	}
}

func TestTemplateTitleURL(t *testing.T) {
	p, _ := newPlugin(map[string]any{"path": "/dl/{{.Title}}"}, nil)
	e := entry.New("Episode One", "http://x.com/a")
	p.(*pathfmtPlugin).Modify(context.Background(), makeCtx(), e) //nolint:errcheck
	if v := e.GetString("download_path"); v != "/dl/Episode One" {
		t.Errorf("download_path: got %q", v)
	}
}

func TestMissingPath(t *testing.T) {
	if _, err := newPlugin(map[string]any{}, nil); err == nil {
		t.Error("expected error when path missing")
	}
}

func TestInvalidTemplate(t *testing.T) {
	if _, err := newPlugin(map[string]any{"path": "{{.Unclosed"}, nil); err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("pathfmt")
	if !ok {
		t.Fatal("pathfmt plugin not registered")
	}
	if d.PluginPhase != plugin.PhaseModify {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}
