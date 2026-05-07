package pathfmt

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func cfg(path, field string) map[string]any {
	return map[string]any{"path": path, "field": field}
}

func TestLiteralPath(t *testing.T) {
	p, err := newPlugin(cfg("/downloads/tv", "download_path"), nil)
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
	p, _ := newPlugin(cfg("/downloads/{{.title}}/Season {{.series_season}}", "download_path"), nil)
	e := entry.New("My Show S02E03", "http://x.com/a")
	e.Set("title", "My Show")
	e.Set("series_season", 2)
	p.(*pathfmtPlugin).Modify(context.Background(), makeCtx(), e) //nolint:errcheck
	if v := e.GetString("download_path"); v != "/downloads/My Show/Season 2" {
		t.Errorf("download_path: got %q", v)
	}
}

func TestTemplateTitleURL(t *testing.T) {
	p, _ := newPlugin(cfg("/dl/{{.Title}}", "download_path"), nil)
	e := entry.New("Episode One", "http://x.com/a")
	p.(*pathfmtPlugin).Modify(context.Background(), makeCtx(), e) //nolint:errcheck
	if v := e.GetString("download_path"); v != "/dl/Episode One" {
		t.Errorf("download_path: got %q", v)
	}
}

func TestScrubsInvalidChars(t *testing.T) {
	p, _ := newPlugin(cfg("/media/{title}", "download_path"), nil)
	e := entry.New("x", "http://x.com/a")
	e.Set("title", "Show: The Movie")
	p.(*pathfmtPlugin).Modify(context.Background(), makeCtx(), e) //nolint:errcheck
	if v := e.GetString("download_path"); v != "/media/Show_ The Movie" {
		t.Errorf("download_path: got %q, want /media/Show_ The Movie", v)
	}
}

func TestCustomOutputField(t *testing.T) {
	p, err := newPlugin(cfg("/mnt/downloads", "my_path"), nil)
	if err != nil {
		t.Fatal(err)
	}
	e := entry.New("x", "http://x.com/a")
	p.(*pathfmtPlugin).Modify(context.Background(), makeCtx(), e) //nolint:errcheck
	if v := e.GetString("my_path"); v != "/mnt/downloads" {
		t.Errorf("my_path: got %q", v)
	}
	if v := e.GetString("download_path"); v != "" {
		t.Errorf("download_path should be empty when field=my_path, got %q", v)
	}
}

func TestMissingPath(t *testing.T) {
	if _, err := newPlugin(map[string]any{"field": "download_path"}, nil); err == nil {
		t.Error("expected error when path missing")
	}
}

func TestMissingField(t *testing.T) {
	if _, err := newPlugin(map[string]any{"path": "/downloads"}, nil); err == nil {
		t.Error("expected error when field missing")
	}
}

func TestInvalidTemplate(t *testing.T) {
	if _, err := newPlugin(map[string]any{"path": "{{.Unclosed", "field": "download_path"}, nil); err == nil {
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
