package regexp

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func run(t *testing.T, cfg map[string]any, e *entry.Entry) *entry.Entry {
	t.Helper()
	p, err := newRegexpPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newRegexpPlugin error: %v", err)
	}
	if err := p.(plugin.FilterPlugin).Filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatalf("Filter error: %v", err)
	}
	return e
}

func TestAcceptPattern(t *testing.T) {
	e := entry.New("Good Show S01E01", "http://x.com")
	run(t, map[string]any{"accept": []any{"(?i)good show"}}, e)
	if !e.IsAccepted() {
		t.Errorf("expected accepted, got %s", e.State)
	}
}

func TestRejectPattern(t *testing.T) {
	e := entry.New("Spam Title", "http://x.com")
	run(t, map[string]any{"reject": []any{"(?i)spam"}}, e)
	if !e.IsRejected() {
		t.Errorf("expected rejected, got %s", e.State)
	}
}

func TestRejectBeatsAccept(t *testing.T) {
	e := entry.New("Good Spam", "http://x.com")
	run(t, map[string]any{
		"accept": []any{"(?i)good"},
		"reject": []any{"(?i)spam"},
	}, e)
	if !e.IsRejected() {
		t.Errorf("reject should beat accept, got %s", e.State)
	}
}

func TestNoMatchOnAcceptListLeavesUndecided(t *testing.T) {
	e := entry.New("Random Title", "http://x.com")
	run(t, map[string]any{"accept": []any{"(?i)specific"}}, e)
	if !e.IsUndecided() {
		t.Errorf("unmatched accept list should leave undecided, got %s", e.State)
	}
}

func TestNoPatternsLeavesUndecided(t *testing.T) {
	e := entry.New("Anything", "http://x.com")
	run(t, map[string]any{}, e)
	if !e.IsUndecided() {
		t.Errorf("no patterns should leave undecided, got %s", e.State)
	}
}

func TestFromUrl(t *testing.T) {
	e := entry.New("Title", "http://tracker.example.com/file.torrent")
	run(t, map[string]any{
		"accept": []any{"tracker\\.example\\.com"},
		"from":   []any{"url"},
	}, e)
	if !e.IsAccepted() {
		t.Errorf("expected accepted by url field, got %s", e.State)
	}
}

func TestFromCustomField(t *testing.T) {
	e := entry.New("Title", "http://x.com")
	e.Set("description", "Contains keyword")
	run(t, map[string]any{
		"accept": []any{"keyword"},
		"from":   []any{"description"},
	}, e)
	if !e.IsAccepted() {
		t.Errorf("expected accepted via custom field, got %s", e.State)
	}
}

func TestBareStringPattern(t *testing.T) {
	e := entry.New("Hello World", "http://x.com")
	run(t, map[string]any{"accept": "Hello"}, e)
	if !e.IsAccepted() {
		t.Errorf("bare string pattern should work, got %s", e.State)
	}
}

func TestInvalidPattern(t *testing.T) {
	_, err := newRegexpPlugin(map[string]any{"accept": []any{"[invalid"}}, nil)
	if err == nil {
		t.Error("expected error for invalid regex pattern")
	}
}

func TestRegistered(t *testing.T) {
	d, ok := plugin.Lookup("regexp")
	if !ok {
		t.Fatal("regexp plugin not registered")
	}
	if d.PluginPhase != plugin.PhaseFilter {
		t.Errorf("want phase filter, got %s", d.PluginPhase)
	}
}

func TestNameAndPhase(t *testing.T) {
	p, _ := newRegexpPlugin(map[string]any{}, nil)
	if p.(*regexpPlugin).Name() != "regexp" {
		t.Error("Name() wrong")
	}
	if p.(*regexpPlugin).Phase() != plugin.PhaseFilter {
		t.Error("Phase() wrong")
	}
}

func TestToStringSliceTypes(t *testing.T) {
	// bare []string
	got, err := toStringSlice([]string{"a", "b"})
	if err != nil || len(got) != 2 {
		t.Errorf("[]string: got %v %v", got, err)
	}

	// []any with non-string element
	_, err = toStringSlice([]any{"ok", 42})
	if err == nil {
		t.Error("expected error for []any with non-string element")
	}

	// unsupported type
	_, err = toStringSlice(123)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestInvalidFromType(t *testing.T) {
	_, err := newRegexpPlugin(map[string]any{"from": 42}, nil)
	if err == nil {
		t.Error("expected error for invalid 'from' type")
	}
}

func TestInvalidRejectPattern(t *testing.T) {
	_, err := newRegexpPlugin(map[string]any{"reject": []any{"[bad"}}, nil)
	if err == nil {
		t.Error("expected error for invalid reject pattern")
	}
}
