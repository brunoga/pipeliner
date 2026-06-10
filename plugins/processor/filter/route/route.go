// Package route provides two internal plugins used together to implement
// conditional entry routing in DAG pipelines.
//
// The "route" processor evaluates an ordered list of named conditions; the
// first match stamps _route_port on the entry and accepts it. Entries that
// match no port are rejected with a warning so they are not silently dropped.
//
// The "route_selector" processor (created automatically by the route() Starlark
// builtin) passes only entries whose _route_port matches a given port name.
//
// Users never instantiate these plugins directly — they use the route()
// builtin in their config:
//
//	meta = process("metainfo_file", upstream=upstream)
//	routes = route(meta,
//	    series = "media_type == 'series'",
//	    movies = "media_type == 'movie'")
//	series_path = process("metainfo_tvdb", upstream=routes.series, api_key=...)
//	movies_path = process("metainfo_tmdb", upstream=routes.movies, api_key=...)
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
		Description: "route entries to named ports based on conditions; unmatched entries are rejected with a warning",
		Role:        plugin.RoleProcessor,
		Produces:    []string{entry.FieldRoutePort},
		// Accept markers so a port expression like `"empty_marker == true"`
		// can route the marker to a dedicated alert branch. The expression
		// evaluator reads e.Fields, so the empty_marker field is visible.
		AcceptsMarkers: true,
		Factory:        newRoutePlugin,
		Validate:       validateRoute,
		Schema: []plugin.FieldSchema{
			{Key: "rules", Type: plugin.FieldTypeRuleList, Required: true, Hint: "Ordered list of named conditions — each entry needs a Name and a Condition expression"},
		},
	})

	plugin.Register(&plugin.Descriptor{
		PluginName:  "route_selector",
		Description: "internal: passes only entries whose _route_port matches the configured port name",
		Role:        plugin.RoleProcessor,
		Requires:    plugin.RequireAll(entry.FieldRoutePort),
		Internal:    true,
		// route stamped the marker with _route_port; route_selector must
		// pass it through to the matching downstream port.
		AcceptsMarkers: true,
		Factory:        newSelectorPlugin,
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "route_selector",
				"_route_port_name", RouteGroupKey,
				"_port_accept_expr")
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
	rules        []routeRule
	referencesSt bool // any port expression references the `state` identifier
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
		if e.ReferencesState() {
			p.referencesSt = true
		}
		p.rules = append(p.rules, routeRule{name: name, expr: e})
	}
	return p, nil
}

func (p *routePlugin) Name() string { return "route" }

// EffectiveInputStates widens the executor's state pre-filter to all four
// states when any port expression references `state`. Without this, an
// entry the user wrote a port for (say `failed = "state == \"failed\""`)
// would be hidden by the default StatesAcceptedUndecided pre-filter. When
// no port references state we keep the default so existing route configs
// behave unchanged.
func (p *routePlugin) EffectiveInputStates() entry.StateSet {
	if p.referencesSt {
		return entry.StatesAll
	}
	return entry.StatesAcceptedUndecided
}

func (p *routePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		// EntryDataWithState exposes the entry's state as the `state`
		// identifier so port expressions can branch on it.
		data := interp.EntryDataWithState(e)
		matched := false
		for _, r := range p.rules {
			ok, err := r.expr.Eval(data)
			if err != nil {
				tc.Logger.Warn("route: expression error", "port", r.name, "entry", e.Title, "err", err)
				continue
			}
			if ok {
				e.Set(entry.FieldRoutePort, r.name)
				// Accept() naively un-Fails Failed entries (the state
				// machine only no-ops for Rejected). When state widening
				// puts a terminal entry in front of route, stamp the port
				// but leave the entry's state as-is — the port assignment
				// is metadata, not a re-acceptance.
				if !e.IsFailed() && !e.IsRejected() {
					e.Accept()
				}
				tc.Logger.Debug("route: entry routed", "port", r.name, "entry", e.Title)
				matched = true
				break
			}
		}
		if !matched {
			// Unmatched-on-default-states keeps the old behavior: reject so
			// the user can see entries fell off the configured ports.
			// Unmatched terminal-state entries (only visible when state
			// widening is active) pass through unchanged — re-rejecting a
			// Failed entry would lose the original failure reason.
			if e.IsFailed() || e.IsRejected() {
				continue
			}
			tc.Logger.Warn("route: no port matched", "entry", e.Title)
			e.Reject("route: no port matched")
		}
	}
	return entry.PassThrough(entries), nil
}

// ── route_selector ───────────────────────────────────────────────────────────

type selectorPlugin struct {
	portName string
}

func newSelectorPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	name, _ := cfg["_route_port_name"].(string)
	if name == "" {
		return nil, fmt.Errorf("route_selector: missing _route_port_name")
	}
	return &selectorPlugin{portName: name}, nil
}

func (p *selectorPlugin) Name() string { return "route_selector" }

func (p *selectorPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	// Filter to only matching entries — do NOT reject non-matching ones.
	// The route node already rejected entries that matched no port; here we are
	// distributing accepted entries across fan-out branches. Calling Reject on
	// the original copy (which storeOutputs passes to the first downstream) would
	// corrupt the pipeline's accepted/rejected summary: the original would be
	// counted as rejected even though a clone on the correct branch was accepted.
	var out []*entry.Entry
	for _, e := range entries {
		if port, _ := e.Get(entry.FieldRoutePort); port == p.portName {
			out = append(out, e)
		}
	}
	return out, nil
}
