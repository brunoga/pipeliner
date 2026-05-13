package accept_all

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func run(t *testing.T, e *entry.Entry) {
	t.Helper()
	p := &acceptAllPlugin{}
	if _, err := p.Process(context.Background(), nil, []*entry.Entry{e}); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
}

func TestAcceptsUndecided(t *testing.T) {
	e := entry.New("Test Entry", "http://example.com")
	run(t, e)
	if !e.IsAccepted() {
		t.Errorf("expected entry to be accepted, got state %s", e.State)
	}
}

func TestDoesNotOverrideAccepted(t *testing.T) {
	e := entry.New("Test Entry", "http://example.com")
	e.Accept()
	run(t, e)
	if !e.IsAccepted() {
		t.Errorf("expected entry to remain accepted, got state %s", e.State)
	}
}

func TestDoesNotOverrideRejected(t *testing.T) {
	e := entry.New("Test Entry", "http://example.com")
	e.Reject("already rejected")
	run(t, e)
	if !e.IsRejected() {
		t.Errorf("expected entry to remain rejected, got state %s", e.State)
	}
	if e.RejectReason != "already rejected" {
		t.Errorf("expected reject reason to be preserved, got %q", e.RejectReason)
	}
}

func TestIsRegistered(t *testing.T) {
	d, ok := plugin.Lookup("accept_all")
	if !ok {
		t.Fatal("accept_all plugin is not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("expected role %v, got %v", plugin.RoleProcessor, d.Role)
	}
}
