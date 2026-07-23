package library_refresh

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/mediaserver"
	"github.com/brunoga/pipeliner/internal/plugin"
)

type fakeClient struct {
	refreshes int
	err       error
}

func (f *fakeClient) ListItems(context.Context) ([]mediaserver.Item, error) { return nil, nil }
func (f *fakeClient) Refresh(context.Context) error                        { f.refreshes++; return f.err }

func accepted(title string) *entry.Entry {
	e := entry.New(title, "https://example.com/"+title)
	e.Accept("test")
	return e
}

func tc(dry bool) *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default(), DryRun: dry}
}

func TestRefreshOncePerRun(t *testing.T) {
	f := &fakeClient{}
	p := &refreshPlugin{backend: "plex", client: f}
	entries := []*entry.Entry{accepted("a"), accepted("b"), accepted("c")}
	if err := p.Consume(context.Background(), tc(false), entries); err != nil {
		t.Fatal(err)
	}
	if f.refreshes != 1 {
		t.Fatalf("want exactly 1 refresh for 3 entries, got %d", f.refreshes)
	}
}

func TestNoAcceptedEntriesNoRefresh(t *testing.T) {
	f := &fakeClient{}
	p := &refreshPlugin{backend: "plex", client: f}
	e := entry.New("x", "https://example.com/x")
	e.Reject("upstream failed it")
	if err := p.Consume(context.Background(), tc(false), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if f.refreshes != 0 {
		t.Fatal("no accepted entries must mean no rescan")
	}
}

func TestDryRunNoRefresh(t *testing.T) {
	f := &fakeClient{}
	p := &refreshPlugin{backend: "jellyfin", client: f}
	e := accepted("a")
	if err := p.Consume(context.Background(), tc(true), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if f.refreshes != 0 {
		t.Fatal("dry-run must not hit the server")
	}
	if !strings.Contains(e.AcceptReason, "would trigger") {
		t.Errorf("dry-run should preview: %q", e.AcceptReason)
	}
}

func TestRefreshFailureDoesNotFailEntries(t *testing.T) {
	f := &fakeClient{err: errors.New("server down")}
	p := &refreshPlugin{backend: "plex", client: f}
	e := accepted("a")
	if err := p.Consume(context.Background(), tc(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("rescan failure must not error the run: %v", err)
	}
	if !e.IsAccepted() {
		t.Fatal("rescan failure must not fail the download entry")
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{"backend": "plex", "url": "http://x", "token": "t"}); len(errs) != 0 {
		t.Errorf("valid config: %v", errs)
	}
	if errs := validate(map[string]any{"backend": "emby", "url": "http://x", "token": "t"}); len(errs) == 0 {
		t.Error("bad backend must fail")
	}
	if errs := validate(map[string]any{"backend": "plex"}); len(errs) == 0 {
		t.Error("missing url/token must fail")
	}
}
