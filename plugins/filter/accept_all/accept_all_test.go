package accept_all

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func TestAcceptsUndecided(t *testing.T) {
	p := &acceptAllPlugin{}
	e := entry.New("Test Entry", "http://example.com")
	if err := p.Filter(context.Background(), nil, e); err != nil {
		t.Fatalf("Filter returned error: %v", err)
	}
	if !e.IsAccepted() {
		t.Errorf("expected entry to be accepted, got state %s", e.State)
	}
}

func TestDoesNotOverrideAccepted(t *testing.T) {
	p := &acceptAllPlugin{}
	e := entry.New("Test Entry", "http://example.com")
	e.Accept()
	if err := p.Filter(context.Background(), nil, e); err != nil {
		t.Fatalf("Filter returned error: %v", err)
	}
	if !e.IsAccepted() {
		t.Errorf("expected entry to remain accepted, got state %s", e.State)
	}
}

func TestDoesNotOverrideRejected(t *testing.T) {
	p := &acceptAllPlugin{}
	e := entry.New("Test Entry", "http://example.com")
	e.Reject("already rejected")
	if err := p.Filter(context.Background(), nil, e); err != nil {
		t.Fatalf("Filter returned error: %v", err)
	}
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
		t.Errorf("expected phase %v, got %v", plugin.RoleProcessor, d.Role)
	}
}
