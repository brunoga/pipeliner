// Package report_empty provides a processor that emits a synthetic marker
// entry when its upstream produced nothing, and emits nothing otherwise.
//
// It exists to opt into "alert when the upstream returned no entries"
// patterns. Per-entry plugins (condition, route, every sink) only run their
// body when they have at least one entry to act on, so an expression like
// `len() == 0` can't fire on an empty batch — there's nothing for the
// expression to be evaluated against. report_empty bridges that gap by
// turning the *absence* of entries into a single entry that downstream
// nodes can react to.
//
// Canonical pattern — fan out from the upstream you want to monitor:
//
//	src    = input("jackett", ..., movie="My Movie")
//	seen   = process("seen",  upstream=src)
//	output("transmission",    upstream=seen, host="localhost")
//
//	alert  = process("report_empty", upstream=src, message="Jackett returned no results")
//	output("notify", via="email", upstream=alert,
//	       to="me@example.com", subject="Pipeline alert", body="{{.Title}}")
//
// The main path (seen → transmission) reads from `src` directly and is
// unaffected by report_empty. The alert path only carries an entry when
// `src` was empty.
package report_empty

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// defaultMessage is the marker's Title when the user didn't set one.
const defaultMessage = "(no entries)"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "report_empty",
		Description: "emit a marker entry when upstream is empty; emit nothing otherwise",
		Role:        plugin.RoleProcessor,
		// MayProduce, not Produces: the marker (and so the field) only
		// appears on the empty-batch branch. Declaring it as Produces would
		// promise the field on every entry the plugin emits, which is
		// trivially true here (the only emitted entry is the marker), but
		// MayProduce models the conditional flow better for downstream
		// Requires checks.
		MayProduce: []string{entry.FieldEmptyMarker},
		// Accept markers so that chaining report_empty after another
		// marker-producing plugin doesn't double-fire — if upstream
		// already produced a marker, this instance correctly sees the
		// batch as "non-empty" and emits nothing.
		AcceptsMarkers: true,
		Factory:        newPlugin,
		Validate:       validate,
		Schema: []plugin.FieldSchema{
			{Key: "message", Type: plugin.FieldTypeString,
				Hint: `Title to set on the marker entry (default: "(no entries)").`},
		},
	})
}

func validate(cfg map[string]any) []error {
	return plugin.OptUnknownKeys(cfg, "report_empty", "message")
}

type reportEmptyPlugin struct {
	message string
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	msg, _ := cfg["message"].(string)
	if msg == "" {
		msg = defaultMessage
	}
	return &reportEmptyPlugin{message: msg}, nil
}

func (p *reportEmptyPlugin) Name() string { return "report_empty" }

func (p *reportEmptyPlugin) Process(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	if len(entries) > 0 {
		// Non-empty: drop everything. report_empty is a marker-only emitter
		// — chain it on a fan-out branch from the upstream you want to
		// monitor; the main path should read from the upstream directly.
		return nil, nil
	}

	// Empty: synthesize a single marker entry, born Accepted so default
	// sinks (notify, print, …) pick it up without needing a filter in
	// between. The URL is synthetic and pipeline-scoped so it doesn't
	// collide with real entries. The marker flag tells the executor to
	// strip this entry from any downstream plugin that hasn't opted in via
	// Descriptor.AcceptsMarkers — so a misrouted tmdb/transmission/etc.
	// won't try to enrich or download the placeholder.
	url := fmt.Sprintf("pipeliner://empty/%s", tc.Name)
	m := entry.New(p.message, url)
	m.Set(entry.FieldEmptyMarker, true)
	m.SetMarker()
	m.Accept()
	return []*entry.Entry{m}, nil
}
