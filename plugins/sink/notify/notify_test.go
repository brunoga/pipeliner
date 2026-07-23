package notify

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	inotify "github.com/brunoga/pipeliner/internal/notify"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// Register a mock notifier for testing.
func init() {
	inotify.Register("mock", inotify.Descriptor{
		Factory: func(_ map[string]any) (inotify.Notifier, error) {
			return &mockNotifier{}, nil
		},
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
	p, err := newPlugin(cfg, nil)
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
	if err := p.deliver(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
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
	if err := p.deliver(context.Background(), makeCtx(), nil); err != nil {
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
	if err := p.deliver(context.Background(), makeCtx(), nil); err != nil {
		t.Fatal(err)
	}
	mn := p.notifier.(*mockNotifier)
	if mn.last.Title != "heartbeat" {
		t.Errorf("expected heartbeat notification, title=%q", mn.last.Title)
	}
}

func TestMissingVia(t *testing.T) {
	_, err := newPlugin(map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error when via is missing")
	}
}

func TestUnknownNotifier(t *testing.T) {
	_, err := newPlugin(map[string]any{"via": "no-such-notifier"}, nil)
	if err == nil {
		t.Fatal("expected error for unknown notifier")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("notify")
	if !ok {
		t.Fatal("notify not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("phase: got %v", d.Role)
	}
}

// ── digest mode ───────────────────────────────────────────────────────────────

type countingNotifier struct {
	sends []inotify.Message
	err   error
}

func (c *countingNotifier) Send(_ context.Context, msg inotify.Message) error {
	if c.err != nil {
		return c.err
	}
	c.sends = append(c.sends, msg)
	return nil
}

func digestPlugin(t *testing.T, window string) (*notifyPlugin, *countingNotifier) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pl, err := newPlugin(map[string]any{"via": "mock", "digest": window}, db)
	if err != nil {
		t.Fatal(err)
	}
	p := pl.(*notifyPlugin)
	n := &countingNotifier{}
	p.notifier = n
	return p, n
}

func digestTC() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "digest-test", Logger: slog.Default()}
}

func acceptedEntry(title string) *entry.Entry {
	e := entry.New(title, "https://example.com/"+title)
	e.Accept("test: matched")
	return e
}

func TestDigestBuffersWithinWindow(t *testing.T) {
	p, n := digestPlugin(t, "24h")
	base := time.Now()
	p.now = func() time.Time { return base }

	// First run initializes the window and buffers.
	if err := p.Consume(context.Background(), digestTC(), []*entry.Entry{acceptedEntry("a")}); err != nil {
		t.Fatal(err)
	}
	// Second run one hour later: still inside the window.
	p.now = func() time.Time { return base.Add(time.Hour) }
	if err := p.Consume(context.Background(), digestTC(), []*entry.Entry{acceptedEntry("b")}); err != nil {
		t.Fatal(err)
	}
	if len(n.sends) != 0 {
		t.Fatalf("nothing should send inside the window, got %d", len(n.sends))
	}

	// Past the window: one combined message with both items.
	p.now = func() time.Time { return base.Add(25 * time.Hour) }
	if err := p.Consume(context.Background(), digestTC(), []*entry.Entry{acceptedEntry("c")}); err != nil {
		t.Fatal(err)
	}
	if len(n.sends) != 1 {
		t.Fatalf("want 1 digest send, got %d", len(n.sends))
	}
	if len(n.sends[0].Entries) != 3 {
		t.Fatalf("digest should carry all 3 buffered entries, got %d", len(n.sends[0].Entries))
	}

	// Buffer cleared after the send.
	p.now = func() time.Time { return base.Add(26 * time.Hour) }
	if err := p.Consume(context.Background(), digestTC(), nil); err != nil {
		t.Fatal(err)
	}
	if len(n.sends) != 1 {
		t.Fatal("no second send right after a flush")
	}
}

func TestDigestEmptyWindowRestartsQuietly(t *testing.T) {
	p, n := digestPlugin(t, "1h")
	base := time.Now()
	p.now = func() time.Time { return base }
	if err := p.Consume(context.Background(), digestTC(), nil); err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return base.Add(2 * time.Hour) }
	if err := p.Consume(context.Background(), digestTC(), nil); err != nil {
		t.Fatal(err)
	}
	if len(n.sends) != 0 {
		t.Fatal("empty windows must not send")
	}
}

func TestDigestSendFailureKeepsBuffer(t *testing.T) {
	p, n := digestPlugin(t, "1h")
	base := time.Now()
	p.now = func() time.Time { return base }
	_ = p.Consume(context.Background(), digestTC(), []*entry.Entry{acceptedEntry("a")})

	n.err = errors.New("smtp down")
	p.now = func() time.Time { return base.Add(2 * time.Hour) }
	if err := p.Consume(context.Background(), digestTC(), nil); err == nil {
		t.Fatal("send failure should propagate")
	}

	// Notifier recovers: the buffered item is still there and sends.
	n.err = nil
	p.now = func() time.Time { return base.Add(4 * time.Hour) }
	if err := p.Consume(context.Background(), digestTC(), nil); err != nil {
		t.Fatal(err)
	}
	if len(n.sends) != 1 || len(n.sends[0].Entries) != 1 {
		t.Fatalf("recovered send should carry the kept buffer: %+v", n.sends)
	}
}

func TestDigestValidation(t *testing.T) {
	if errs := validate(map[string]any{"via": "mock", "digest": "not-a-duration"}); len(errs) == 0 {
		t.Error("bad digest duration must fail validation")
	}
	if errs := validate(map[string]any{"via": "mock", "digest": "24h"}); len(errs) != 0 {
		t.Errorf("valid digest config: %v", errs)
	}
}
