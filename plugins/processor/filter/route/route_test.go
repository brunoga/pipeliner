package route

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func makeRoute(t *testing.T, rules []map[string]any) *routePlugin {
	t.Helper()
	rawRules := make([]any, len(rules))
	for i, r := range rules {
		rawRules[i] = map[string]any(r)
	}
	p, err := newRoutePlugin(map[string]any{"rules": rawRules}, nil)
	if err != nil {
		t.Fatalf("newRoutePlugin: %v", err)
	}
	return p.(*routePlugin)
}

func makeSelector(t *testing.T, portName string) *selectorPlugin {
	t.Helper()
	p, err := newSelectorPlugin(map[string]any{"_route_port_name": portName}, nil)
	if err != nil {
		t.Fatalf("newSelectorPlugin: %v", err)
	}
	return p.(*selectorPlugin)
}

func TestRouteMatchesFirstPort(t *testing.T) {
	p := makeRoute(t, []map[string]any{
		{"name": "series", "accept": "series_episode_id != ''"},
		{"name": "movies", "accept": "true"},
	})

	e := entry.New("Breaking.Bad.S01E01.720p", "http://example.com/1")
	e.Set("series_episode_id", "S01E01")

	_, err := p.Process(context.Background(), tc(), []*entry.Entry{e})
	if err != nil {
		t.Fatal(err)
	}

	if !e.IsAccepted() {
		t.Error("matched entry should be accepted")
	}
	port, _ := e.Get(entry.FieldRoutePort)
	if port != "series" {
		t.Errorf("_route_port: got %q, want %q", port, "series")
	}
}

func TestRouteFallsToSecondPort(t *testing.T) {
	p := makeRoute(t, []map[string]any{
		{"name": "series", "accept": "series_episode_id != ''"},
		{"name": "movies", "accept": "true"},
	})

	e := entry.New("Inception.2010.1080p.BluRay", "http://example.com/2")
	// no series_episode_id set

	p.Process(context.Background(), tc(), []*entry.Entry{e})
	if !e.IsAccepted() {
		t.Error("fallback port should accept the entry")
	}
	port, _ := e.Get(entry.FieldRoutePort)
	if port != "movies" {
		t.Errorf("_route_port: got %q, want %q", port, "movies")
	}
}

func TestRouteRejectsUnmatched(t *testing.T) {
	p := makeRoute(t, []map[string]any{
		{"name": "series", "accept": "series_episode_id != ''"},
	})

	e := entry.New("Inception.2010.1080p.BluRay", "http://example.com/3")

	p.Process(context.Background(), tc(), []*entry.Entry{e})
	if !e.IsRejected() {
		t.Error("unmatched entry should be rejected")
	}
	if e.RejectReason != "route: no port matched" {
		t.Errorf("reject reason: got %q", e.RejectReason)
	}
}

func TestSelectorPassesMatchingPort(t *testing.T) {
	sel := makeSelector(t, "series")

	e := entry.New("Breaking.Bad.S01E01", "http://example.com/1")
	e.Set(entry.FieldRoutePort, "series")
	e.Accept()

	sel.Process(context.Background(), tc(), []*entry.Entry{e})
	if !e.IsAccepted() {
		t.Error("matching port entry should remain accepted")
	}
}

func TestSelectorFiltersNonMatchingPort(t *testing.T) {
	sel := makeSelector(t, "series")

	e := entry.New("Inception.2010", "http://example.com/2")
	e.Set(entry.FieldRoutePort, "movies")
	e.Accept()

	out, _ := sel.Process(context.Background(), tc(), []*entry.Entry{e})
	// Non-matching entries are filtered out (not returned) rather than rejected.
	// Rejecting them would corrupt the pipeline accepted/rejected count because
	// the executor passes the original entry to the first route_selector even when
	// a clone will be accepted by the correct selector on another branch.
	if len(out) != 0 {
		t.Errorf("non-matching port entry should be absent from output, got %d entries", len(out))
	}
	if e.IsRejected() {
		t.Error("non-matching port entry should NOT be rejected (state must not be mutated)")
	}
}

func TestRoutePluginsRegistered(t *testing.T) {
	for _, name := range []string{"route", "route_selector"} {
		if _, ok := plugin.Lookup(name); !ok {
			t.Errorf("plugin %q not registered", name)
		}
	}
}

func TestRouteStateIdentifier(t *testing.T) {
	// A port that branches on `state` should widen the plugin's effective
	// InputStates so the executor stops hiding non-default-state entries,
	// and the port should match the corresponding entries.
	p := makeRoute(t, []map[string]any{
		{"name": "failed_alert", "accept": `state == "failed"`},
		{"name": "ok", "accept": `state == "accepted"`},
	})
	if !p.referencesSt {
		t.Fatal("plugin should detect state reference in port expression")
	}
	if got := p.EffectiveInputStates(); got != entry.StatesAll {
		t.Errorf("EffectiveInputStates() = %v; want StatesAll", got)
	}

	failed := entry.New("a", "http://x/a")
	failed.Fail("disk full")
	acc := entry.New("b", "http://x/b")
	acc.Accept()

	p.Process(context.Background(), tc(), []*entry.Entry{failed, acc})

	if port, _ := failed.Get(entry.FieldRoutePort); port != "failed_alert" {
		t.Errorf("Failed entry: _route_port=%q; want failed_alert", port)
	}
	if !failed.IsFailed() {
		t.Errorf("Failed entry must stay Failed after routing, got %v", failed.State)
	}
	if failed.FailReason != "disk full" {
		t.Errorf("FailReason mutated to %q; expected original to be preserved", failed.FailReason)
	}

	if port, _ := acc.Get(entry.FieldRoutePort); port != "ok" {
		t.Errorf("Accepted entry: _route_port=%q; want ok", port)
	}
	if !acc.IsAccepted() {
		t.Errorf("Accepted entry must stay Accepted, got %v", acc.State)
	}
}

func TestRouteUnmatchedTerminalStatePassesThrough(t *testing.T) {
	// A Failed entry that doesn't match any port must NOT be re-rejected —
	// that would overwrite the failure reason. Only entries in the default
	// states (Accepted/Undecided) get the legacy "no match → reject"
	// treatment.
	p := makeRoute(t, []map[string]any{
		{"name": "ok", "accept": `state == "accepted"`},
	})

	failed := entry.New("a", "http://x/a")
	failed.Fail("disk full")

	p.Process(context.Background(), tc(), []*entry.Entry{failed})

	if !failed.IsFailed() {
		t.Errorf("Failed entry must stay Failed when no port matches, got %v", failed.State)
	}
	if failed.FailReason != "disk full" {
		t.Errorf("FailReason mutated to %q; expected original to be preserved", failed.FailReason)
	}
	if port, _ := failed.Get(entry.FieldRoutePort); port != nil {
		t.Errorf("unmatched Failed entry should have no _route_port set, got %v", port)
	}
}

func TestRouteNoStateRefKeepsDefaultInputStates(t *testing.T) {
	p := makeRoute(t, []map[string]any{
		{"name": "all", "accept": "true"},
	})
	if p.referencesSt {
		t.Fatal("plugin should NOT detect state reference in a field-only expression")
	}
	if got := p.EffectiveInputStates(); got != entry.StatesAcceptedUndecided {
		t.Errorf("EffectiveInputStates() = %v; want StatesAcceptedUndecided", got)
	}
}
