// Package route provides two internal plugins used together to implement
// conditional entry routing in DAG pipelines.
//
// The "route" processor evaluates an ordered list of named conditions; the
// first match stamps _route_leg on the entry and accepts it. Entries that
// match no leg are rejected with a warning so they are not silently dropped.
//
// The "route_selector" processor (created automatically by the route() Starlark
// builtin) passes only entries whose _route_leg matches a given leg name.
//
// Users never instantiate these plugins directly — they use the route()
// builtin in their config:
//
//	routes = route(upstream,
//	    series = "series_episode_id != ''",
//	    movies = "series_episode_id == ''")
//	series_path = process("metainfo_series", upstream=routes.series)
//	movies_path = process("metainfo_tmdb",   upstream=routes.movies, ...)
package route

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/expr"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// RouteGroupKey is the config key used to tag route and route_selector nodes
// with their shared group ID. The DAG validator uses this to detect
// mutually-exclusive merge points and apply union semantics for certain fields.
const RouteGroupKey = "_route_group"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "route",
		Description: "route entries to named legs based on conditions; unmatched entries are rejected with a warning",
		Role:        plugin.RoleProcessor,
		Produces:    []string{entry.FieldRouteLeg},
		Factory:     newRoutePlugin,
		Validate:    validateRoute,
		Schema: []plugin.FieldSchema{
			{Key: "rules", Type: plugin.FieldTypeList, Required: true, Hint: "Ordered list of {name, accept} rule objects"},
		},
	})

	plugin.Register(&plugin.Descriptor{
		PluginName:  "route_selector",
		Description: "internal: passes only entries whose _route_leg matches the configured leg name",
		Role:        plugin.RoleProcessor,
		Requires:    plugin.RequireAll(entry.FieldRouteLeg),
		Internal:    true,
		Factory:     newSelectorPlugin,
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "route_selector", "_route_leg_name", RouteGroupKey)
		},
	})
}

func validateRoute(cfg map[string]any) []error {
	var errs []error
	if rules, _ := cfg["rules"].([]any); len(rules) == 0 {
		errs = append(errs, fmt.Errorf("route: 'rules' must be a non-empty list"))
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "route", "rules", RouteGroupKey)...)
	return errs
}

// ── route processor ──────────────────────────────────────────────────────────

type routeRule struct {
	name string
	expr *expr.Expr
}

type routePlugin struct {
	rules []routeRule
}

func newRoutePlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	rawRules, _ := cfg["rules"].([]any)
	if len(rawRules) == 0 {
		return nil, fmt.Errorf("route: 'rules' must be a non-empty list")
	}
	p := &routePlugin{}
	for i, r := range rawRules {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("route: rules[%d] must be a map with 'name' and 'accept' keys", i)
		}
		name, _ := m["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("route: rules[%d] missing 'name'", i)
		}
		acceptStr, _ := m["accept"].(string)
		if acceptStr == "" {
			return nil, fmt.Errorf("route: rules[%d] missing 'accept' expression", i)
		}
		e, err := expr.Compile(acceptStr)
		if err != nil {
			return nil, fmt.Errorf("route: rules[%d] invalid accept expression: %w", i, err)
		}
		p.rules = append(p.rules, routeRule{name: name, expr: e})
	}
	return p, nil
}

func (p *routePlugin) Name() string { return "route" }

func (p *routePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		data := interp.EntryData(e)
		matched := false
		for _, r := range p.rules {
			ok, err := r.expr.Eval(data)
			if err != nil {
				tc.Logger.Warn("route: expression error", "leg", r.name, "entry", e.Title, "err", err)
				continue
			}
			if ok {
				e.Set(entry.FieldRouteLeg, r.name)
				e.Accept()
				matched = true
				break
			}
		}
		if !matched {
			tc.Logger.Warn("route: no leg matched", "entry", e.Title)
			e.Reject("route: no leg matched")
		}
	}
	return entry.PassThrough(entries), nil
}

// ── route_selector ───────────────────────────────────────────────────────────

type selectorPlugin struct {
	legName string
}

func newSelectorPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	name, _ := cfg["_route_leg_name"].(string)
	if name == "" {
		return nil, fmt.Errorf("route_selector: missing _route_leg_name")
	}
	return &selectorPlugin{legName: name}, nil
}

func (p *selectorPlugin) Name() string { return "route_selector" }

func (p *selectorPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if leg, _ := e.Get(entry.FieldRouteLeg); leg != p.legName {
			e.Reject(fmt.Sprintf("route_selector: entry belongs to leg %q, not %q", leg, p.legName))
		}
	}
	return entry.PassThrough(entries), nil
}
