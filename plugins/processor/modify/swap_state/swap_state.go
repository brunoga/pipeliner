// Package swap_state provides a processor plugin that swaps entries between
// two states. It is one of the few plugins that intentionally operates on
// rejected and failed entries — its purpose is to flip them back into a
// state where downstream nodes can act on them.
//
// Use cases:
//
//   - cleanup pipelines: feed dedup's rejected losers into a delete sink while
//     the accepted winners are diverted away from the same sink:
//
//	dedup → swap_state(swap=["accepted", "rejected"]) → exec("rm {file_location}")
//
//   - retry pipelines: rescue failed entries through an alternate sink:
//
//	primary_sink → swap_state(swap=["accepted", "failed"]) → fallback_sink
//
//   - re-feeding rejected entries through downstream filters:
//
//	condition(reject="x") → swap_state(swap=["rejected", "undecided"]) → another_filter
//
// AcceptReason / RejectReason / FailReason on the entry are preserved across
// the swap — they document why the entry first entered its prior state and
// remain accessible as audit history. The Entry.LastStateChange field records
// the swap itself (which two states, which plugin, when) so notify templates
// and downstream logic can distinguish "this was always accepted" from "this
// was rescued by a swap".
package swap_state

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "swap_state"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "swap entries between two states (e.g. accepted ↔ rejected) so downstream nodes can act on entries others rejected or failed",
		Role:        plugin.RoleProcessor,
		// Declare access to every state — the whole point of swap_state is to
		// operate on entries other plugins have terminally rejected or failed,
		// so the default processor pre-filter (StatesAcceptedUndecided) would
		// hide exactly the entries this plugin needs to act on.
		InputStates: entry.StatesAll,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "swap", Type: plugin.FieldTypeList, Required: true,
				Hint: "Two distinct states from {accepted, rejected, failed, undecided}; entries in either state are flipped to the other"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if _, _, err := parseSwap(cfg); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, "swap")...)
	return errs
}

type swapStatePlugin struct {
	a, b entry.State
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	a, b, err := parseSwap(cfg)
	if err != nil {
		return nil, err
	}
	return &swapStatePlugin{a: a, b: b}, nil
}

func (p *swapStatePlugin) Name() string { return pluginName }

// Process walks every entry — including rejected and failed ones — and flips
// the state of any entry currently in state a or state b. Entries in other
// states are left alone. The Consumed flag is orthogonal to State and is
// likewise untouched.
//
// Unlike most processors this method intentionally does NOT skip-guard on
// IsRejected / IsFailed: its whole purpose is to act on entries other plugins
// have terminally decided about.
func (p *swapStatePlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	now := time.Now()
	for _, e := range entries {
		var to entry.State
		switch e.State {
		case p.a:
			to = p.b
		case p.b:
			to = p.a
		default:
			continue
		}
		from := e.State
		e.State = to
		e.LastStateChange = &entry.StateChange{
			From:   from,
			To:     to,
			Plugin: pluginName,
			Reason: fmt.Sprintf("swap [%s, %s]", p.a, p.b),
			At:     now,
		}
	}
	return entries, nil
}

// parseSwap reads cfg["swap"] and returns the two distinct entry.State values.
// Returns an error for any malformed input: missing key, wrong arity, unknown
// state name, or both elements the same.
func parseSwap(cfg map[string]any) (entry.State, entry.State, error) {
	raw, ok := cfg["swap"]
	if !ok {
		return 0, 0, fmt.Errorf("%s: \"swap\" is required", pluginName)
	}
	names := plugin.ToStringSlice(raw)
	if len(names) != 2 {
		return 0, 0, fmt.Errorf("%s: \"swap\" must list exactly two states, got %d", pluginName, len(names))
	}
	a, err := parseStateName(names[0])
	if err != nil {
		return 0, 0, err
	}
	b, err := parseStateName(names[1])
	if err != nil {
		return 0, 0, err
	}
	if a == b {
		return 0, 0, fmt.Errorf("%s: \"swap\" states must be distinct, got [%s, %s]", pluginName, names[0], names[1])
	}
	return a, b, nil
}

func parseStateName(s string) (entry.State, error) {
	switch s {
	case "accepted":
		return entry.Accepted, nil
	case "rejected":
		return entry.Rejected, nil
	case "failed":
		return entry.Failed, nil
	case "undecided":
		return entry.Undecided, nil
	}
	return 0, fmt.Errorf("%s: unknown state %q (want accepted, rejected, failed, or undecided)", pluginName, s)
}
