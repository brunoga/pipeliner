package notify

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	inotify "github.com/brunoga/pipeliner/internal/notify"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// Register a mock notifier for testing.
func init() {
	inotify.Register("mock", func(_ map[string]any) (inotify.Notifier, error) {
		return &mockNotifier{}, nil
	})
}

type mockNotifier struct {
	last inotify.Message
}

func (m *mockNotifier) Send(_ context.Context, msg inotify.Message) error {
	m.last = msg
	return nil
}

func openPlugin(t *testing.T, cfg map[string]any) *notifyPlugin {
	t.Helper()
	p, err := newPlugin(cfg)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*notifyPlugin)
}

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func TestOutputSendsNotification(t *testing.T) {
	p := openPlugin(t, map[string]any{
		"via":    "mock",
		"config": map[string]any{},
		"title":  "Got {{len .Entries}} items",
	})

	e := entry.New("Test Entry", "http://example.com")
	if err := p.Output(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}

	mn := p.notifier.(*mockNotifier)
	if mn.last.Title != "Got 1 items" {
		t.Errorf("title: got %q", mn.last.Title)
	}
	if len(mn.last.Entries) != 1 {
		t.Errorf("entries: got %d", len(mn.last.Entries))
	}
}

func TestOutputSkipsWhenEmpty(t *testing.T) {
	p := openPlugin(t, map[string]any{"via": "mock", "config": map[string]any{}})
	// No error, no notification sent.
	if err := p.Output(context.Background(), makeCtx(), nil); err != nil {
		t.Fatal(err)
	}
	mn := p.notifier.(*mockNotifier)
	if mn.last.Title != "" {
		t.Error("expected no notification for empty entry list")
	}
}

func TestOutputOnAllSendsWhenEmpty(t *testing.T) {
	p := openPlugin(t, map[string]any{
		"via":    "mock",
		"config": map[string]any{},
		"on":     "all",
		"title":  "heartbeat",
	})
	if err := p.Output(context.Background(), makeCtx(), nil); err != nil {
		t.Fatal(err)
	}
	mn := p.notifier.(*mockNotifier)
	if mn.last.Title != "heartbeat" {
		t.Errorf("expected heartbeat notification, title=%q", mn.last.Title)
	}
}

func TestMissingVia(t *testing.T) {
	_, err := newPlugin(map[string]any{})
	if err == nil {
		t.Fatal("expected error when via is missing")
	}
}

func TestUnknownNotifier(t *testing.T) {
	_, err := newPlugin(map[string]any{"via": "no-such-notifier"})
	if err == nil {
		t.Fatal("expected error for unknown notifier")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("notify")
	if !ok {
		t.Fatal("notify not registered")
	}
	if d.PluginPhase != plugin.PhaseOutput {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}
