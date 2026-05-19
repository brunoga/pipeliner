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

	p.Process(context.Background(), tc(), []*entry.Entry{e}) //nolint:errcheck
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

	p.Process(context.Background(), tc(), []*entry.Entry{e}) //nolint:errcheck
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

	sel.Process(context.Background(), tc(), []*entry.Entry{e}) //nolint:errcheck
	if !e.IsAccepted() {
		t.Error("matching port entry should remain accepted")
	}
}

func TestSelectorRejectsNonMatchingPort(t *testing.T) {
	sel := makeSelector(t, "series")

	e := entry.New("Inception.2010", "http://example.com/2")
	e.Set(entry.FieldRoutePort, "movies")
	e.Accept()

	sel.Process(context.Background(), tc(), []*entry.Entry{e}) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("non-matching port entry should be rejected by selector")
	}
}

func TestRoutePluginsRegistered(t *testing.T) {
	for _, name := range []string{"route", "route_selector"} {
		if _, ok := plugin.Lookup(name); !ok {
			t.Errorf("plugin %q not registered", name)
		}
	}
}
