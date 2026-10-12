package dag

import (
	"fmt"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// validateRunOn checks the run_on key on every node of a graph.
//
// run_on says which triggers a source should run on, so a push to an ingest
// queue stops dragging a pipeline's unrelated feeds along with it. Three
// things can go wrong with it, and all three are caught at config load rather
// than discovered as a pipeline quietly doing nothing:
//
//   - a token this executor does not understand, which would otherwise read
//     as "never run";
//   - run_on on a node that is not a source, which the executor ignores;
//   - a trigger no source admits, which makes the whole pipeline a no-op for
//     that trigger -- the shape a typo takes.
func validateRunOn(g *Graph, reg func(name string) (*plugin.Descriptor, bool)) (errs, warnings []error) {
	// Which trigger kinds at least one source of this graph admits.
	admits := map[plugin.TriggerKind]bool{}
	sources := 0

	for _, n := range g.Nodes() {
		d, ok := reg(n.PluginName)
		if !ok {
			continue // unknown plugin: reported by the main validator
		}
		runOn, err := plugin.RunOnOf(n.Config)
		if err != nil {
			errs = append(errs, fmt.Errorf("node %q (plugin %q): %w", n.ID, n.PluginName, err))
			continue
		}
		isSource := d.EffectiveRole() == plugin.RoleSource
		if isSource {
			sources++
		}
		if len(runOn) == 0 {
			if isSource {
				for _, k := range plugin.RunOnKinds(nil) {
					admits[k] = true
				}
			}
			continue
		}
		if !isSource {
			errs = append(errs, fmt.Errorf(
				"node %q (plugin %q, role %s): %q applies to source nodes only — a processor that stopped filtering depending on why the run happened would be a worse trap than the one run_on exists to fix",
				n.ID, n.PluginName, d.EffectiveRole(), plugin.RunOnKey))
			continue
		}
		if bad, ok := plugin.ValidRunOn(runOn); !ok {
			errs = append(errs, fmt.Errorf(
				"node %q (plugin %q): %q value %q is not a trigger — use schedule, manual, webhook, webhook:<queue>, startup or any",
				n.ID, n.PluginName, plugin.RunOnKey, bad))
			continue
		}
		for _, k := range plugin.RunOnKinds(runOn) {
			admits[k] = true
		}
	}

	if sources == 0 {
		return errs, warnings
	}
	// A trigger no source admits is a pipeline that cannot do anything when
	// it fires. Scheduled and startup fires are only worth reporting when the
	// graph is actually scheduled, which the graph itself does not know, so
	// all four are reported the same way: as a warning naming the trigger.
	for _, k := range []plugin.TriggerKind{
		plugin.TriggerSchedule, plugin.TriggerManual, plugin.TriggerWebhook, plugin.TriggerStartup,
	} {
		if !admits[k] {
			warnings = append(warnings, fmt.Errorf(
				"no source runs on a %s trigger (every source's %q excludes it), so the pipeline produces nothing when one fires",
				k, plugin.RunOnKey))
		}
	}
	return errs, warnings
}
