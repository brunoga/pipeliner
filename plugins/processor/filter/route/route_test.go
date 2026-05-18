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

func makeSelector(t *testing.T, legName string) *selectorPlugin {
	t.Helper()
	p, err := newSelectorPlugin(map[string]any{"_route_leg_name": legName}, nil)
	if err != nil {
		t.Fatalf("newSelectorPlugin: %v", err)
	}
	return p.(*selectorPlugin)
}

func TestRouteMatchesFirstLeg(t *testing.T) {
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
	leg, _ := e.Get(entry.FieldRouteLeg)
	if leg != "series" {
		t.Errorf("_route_leg: got %q, want %q", leg, "series")
	}
}

func TestRouteFallsToSecondLeg(t *testing.T) {
	p := makeRoute(t, []map[string]any{
		{"name": "series", "accept": "series_episode_id != ''"},
		{"name": "movies", "accept": "true"},
	})

	e := entry.New("Inception.2010.1080p.BluRay", "http://example.com/2")
	// no series_episode_id set

	p.Process(context.Background(), tc(), []*entry.Entry{e}) //nolint:errcheck
	if !e.IsAccepted() {
		t.Error("fallback leg should accept the entry")
	}
	leg, _ := e.Get(entry.FieldRouteLeg)
	if leg != "movies" {
		t.Errorf("_route_leg: got %q, want %q", leg, "movies")
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
	if e.RejectReason != "route: no leg matched" {
		t.Errorf("reject reason: got %q", e.RejectReason)
	}
}

func TestSelectorPassesMatchingLeg(t *testing.T) {
	sel := makeSelector(t, "series")

	e := entry.New("Breaking.Bad.S01E01", "http://example.com/1")
	e.Set(entry.FieldRouteLeg, "series")
	e.Accept()

	sel.Process(context.Background(), tc(), []*entry.Entry{e}) //nolint:errcheck
	if !e.IsAccepted() {
		t.Error("matching leg entry should remain accepted")
	}
}

func TestSelectorRejectsNonMatchingLeg(t *testing.T) {
	sel := makeSelector(t, "series")

	e := entry.New("Inception.2010", "http://example.com/2")
	e.Set(entry.FieldRouteLeg, "movies")
	e.Accept()

	sel.Process(context.Background(), tc(), []*entry.Entry{e}) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("non-matching leg entry should be rejected by selector")
	}
}

func TestRoutePluginsRegistered(t *testing.T) {
	for _, name := range []string{"route", "route_selector"} {
		if _, ok := plugin.Lookup(name); !ok {
			t.Errorf("plugin %q not registered", name)
		}
	}
}
