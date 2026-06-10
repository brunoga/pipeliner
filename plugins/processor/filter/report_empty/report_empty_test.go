package report_empty

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func tc() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func newReportEmpty(t *testing.T, cfg map[string]any) *reportEmptyPlugin {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*reportEmptyPlugin)
}

func TestEmptyUpstreamProducesMarker(t *testing.T) {
	p := newReportEmpty(t, nil)
	out, err := p.Process(context.Background(), tc(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 marker entry, got %d", len(out))
	}
	m := out[0]
	if !m.IsAccepted() {
		t.Errorf("marker must be Accepted so default sinks pick it up, got %v", m.State)
	}
	if m.Title != defaultMessage {
		t.Errorf("marker Title = %q; want default %q", m.Title, defaultMessage)
	}
	if v, _ := m.Get(entry.FieldEmptyMarker); v != true {
		t.Errorf("marker should carry %s=true, got %v", entry.FieldEmptyMarker, v)
	}
	if m.URL == "" {
		t.Error("marker URL must not be empty — sinks log/template it")
	}
}

func TestEmptyUpstreamUsesConfiguredMessage(t *testing.T) {
	p := newReportEmpty(t, map[string]any{"message": "Jackett returned no results"})
	out, err := p.Process(context.Background(), tc(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "Jackett returned no results" {
		t.Errorf("marker Title = %q; want configured message", out[0].Title)
	}
}

func TestNonEmptyUpstreamDropsEverything(t *testing.T) {
	// Marker-only emitter: when entries flow in, nothing flows out. The
	// main path of the pipeline must read from the upstream directly, not
	// from report_empty's output — these tests pin that contract.
	p := newReportEmpty(t, nil)
	e1 := entry.New("Movie A", "http://x/a")
	e2 := entry.New("Movie B", "http://x/b")
	out, err := p.Process(context.Background(), tc(), []*entry.Entry{e1, e2})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("non-empty upstream must produce 0 output entries, got %d", len(out))
	}
}

func TestMarkerURLIsTaskScoped(t *testing.T) {
	// The synthetic URL embeds the task name so multiple pipelines reusing
	// report_empty don't collide on a single shared URL — useful for sinks
	// (like seen) that key on URL.
	p := newReportEmpty(t, nil)
	out, err := p.Process(context.Background(), &plugin.TaskContext{Name: "movies-3d"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 marker, got %d", len(out))
	}
	want := "pipeliner://empty/movies-3d"
	if out[0].URL != want {
		t.Errorf("marker URL = %q; want %q", out[0].URL, want)
	}
}

func TestValidateRejectsUnknownKeys(t *testing.T) {
	errs := validate(map[string]any{"unknown_key": "x"})
	if len(errs) == 0 {
		t.Error("validate() should reject unknown config keys")
	}
}
