// Package executor runs a DAG pipeline graph, feeding entries from source
// nodes through processor nodes to sink nodes.
//
// Execution order:
//  1. Compute topological layers via dag.Graph.Layers().
//  2. Execute layers in order; within a layer all nodes run serially.
//  3. For each node, collect entries from all upstream nodes (merge + dedup
//     by URL), dispatch to the plugin's role interface, then invoke it.
//  4. When a node's output fans out to more than one downstream consumer,
//     entries are cloned per consumer so each branch has independent copies.
//  5. After the main loop, a commit phase calls CommitPlugin.Commit for each
//     processor node that implements CommitPlugin, passing only entries whose
//     URL was not failed by any downstream sink across all fan-out branches.
//
// All plugins must implement one of the three role interfaces:
// SourcePlugin.Generate, ProcessorPlugin.Process, or SinkPlugin.Consume.
//
// Sink chaining: a sink node may have downstream sink nodes. Each sink applies
// its declared InputStates pre-filter (default RoleSink: StatesAcceptedOnly)
// plus an always-on consumed-exclusion at the sink boundary, so chained sinks
// naturally only see entries the upstream sink accepted. No special case in
// the executor's chained-sink path — the per-sink pre-filter is the gate.
package executor

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// PluginInstance pairs a resolved plugin with its per-node config.
type PluginInstance struct {
	Desc   *plugin.Descriptor
	Impl   plugin.Plugin
	Config map[string]any
}

// effectiveInputStates returns the state set the executor should pre-filter
// upstream entries to before calling this plugin instance. A plugin that
// implements plugin.DynamicInputStates wins over its Descriptor's static
// declaration — that's how condition and route widen themselves when their
// expressions reference the `state` identifier, without statically claiming
// all four states for every instance.
func effectiveInputStates(pi *PluginInstance) entry.StateSet {
	if dis, ok := pi.Impl.(plugin.DynamicInputStates); ok {
		return dis.EffectiveInputStates()
	}
	return pi.Desc.EffectiveInputStates()
}

// edgeKey identifies the directed edge from a producer node to a consumer node.
// Entries are stored per-edge so fan-out branches each get their own slice.
type edgeKey struct{ from, to dag.NodeID }

// Executor runs a DAG pipeline.
type Executor struct {
	name           string
	graph          *dag.Graph
	plugins        map[dag.NodeID]*PluginInstance
	logger         *slog.Logger
	dryRun         bool
	validateFields bool
}

// New constructs an Executor. plugins maps each NodeID to its instantiated plugin.
func New(
	name string,
	graph *dag.Graph,
	plugins map[dag.NodeID]*PluginInstance,
	_ *store.SQLiteStore, // reserved for future use
	logger *slog.Logger,
	dryRun bool,
) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{
		name:    name,
		graph:   graph,
		plugins: plugins,
		logger:  logger.With("task", name),
		dryRun:  dryRun,
	}
}

// Name returns the pipeline name.
func (ex *Executor) Name() string { return ex.name }

// SetDryRun enables or disables dry-run mode on a running executor.
func (ex *Executor) SetDryRun(v bool) { ex.dryRun = v }

// SetValidateFields enables per-entry field validation before each node runs.
// When true, the executor warns whenever an entry is missing all fields in a
// Requires group, helping diagnose misconfigured pipelines at runtime.
func (ex *Executor) SetValidateFields(v bool) { ex.validateFields = v }

// Shutdown calls Shutdown() on any plugin that implements plugin.ShutdownPlugin.
func (ex *Executor) Shutdown() {
	for _, pi := range ex.plugins {
		if s, ok := pi.Impl.(plugin.ShutdownPlugin); ok {
			s.Shutdown()
		}
	}
}

// Run executes the pipeline and returns a Result.
func (ex *Executor) Run(ctx context.Context) (*Result, error) {
	start := time.Now()
	if ex.dryRun {
		ex.logger.Info("pipeline started (DRY RUN)")
	} else {
		ex.logger.Info("pipeline started")
	}

	layers, err := ex.graph.Layers()
	if err != nil {
		return nil, fmt.Errorf("executor: topological sort: %w", err)
	}

	// edge[{from, to}] holds entries produced by 'from' specifically for 'to'.
	// Fan-out branches each get an independent slice (cloned if needed).
	edge := make(map[edgeKey][]*entry.Entry)

	res := &Result{NodeResults: make(map[dag.NodeID]*NodeResult, ex.graph.Len())}

	// Track all source entries for total/state accounting.
	var sourceEntries []*entry.Entry

	// failedURLs tracks URLs of entries that were failed by any sink node.
	// Used during the commit phase to exclude them from CommitPlugin.Commit calls.
	failedURLs := map[string]bool{}

	// producedByNode tracks entries produced by each processor node.
	// Used during the commit phase to find entries eligible for committing.
	producedByNode := map[dag.NodeID][]*entry.Entry{}

	for _, layer := range layers {
		for _, n := range layer {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			pi, ok := ex.plugins[n.ID]
			if !ok {
				return nil, fmt.Errorf("executor: no plugin instance for node %q", n.ID)
			}

			upstream := ex.collectUpstream(n, edge)
			produced, nodeErr := ex.runNode(ctx, n, pi, upstream)

			nr := &NodeResult{In: len(upstream), Out: len(produced), Err: nodeErr}
			if len(upstream) > len(produced) {
				nr.Dropped = len(upstream) - len(produced)
			}
			res.NodeResults[n.ID] = nr

			role := pi.Desc.EffectiveRole()
			if role == plugin.RoleSource {
				sourceEntries = append(sourceEntries, produced...)
			}

			// Track entries produced by processor nodes for the commit phase.
			if role == plugin.RoleProcessor {
				producedByNode[n.ID] = produced
			}

			// After a sink runs, collect any entries it failed (by URL).
			// runNode for sinks calls Consume which may mutate entry state via e.Fail().
			// Skip empty URLs so future plugins that produce URL-less entries
			// don't all collide on the same map key.
			if role == plugin.RoleSink {
				for _, e := range upstream {
					if e.IsFailed() && e.URL != "" {
						failedURLs[e.URL] = true
					}
				}
			}

			ex.storeOutputs(n, produced, edge)
		}
	}

	// Commit phase: call CommitPlugin.Commit for all processor nodes that
	// implement CommitPlugin, passing only entries not failed by any sink.
	// Skipped entirely in dry-run mode so tracker state (seen, movies,
	// series, premiere) doesn't advance — without this skip, dry-run would
	// suppress sink side effects but still leave the next real run thinking
	// those entries had already been processed.
	if ex.dryRun {
		ex.logger.Debug("dry-run: skipping commit phase")
	} else {
	commitLoop:
		for _, layer := range layers {
			for _, n := range layer {
				if ctx.Err() != nil {
					break commitLoop
				}
				pi, ok := ex.plugins[n.ID]
				if !ok {
					continue
				}
				if pi.Desc.EffectiveRole() != plugin.RoleProcessor {
					continue
				}
				cp, ok := pi.Impl.(plugin.CommitPlugin)
				if !ok {
					continue
				}
				produced := producedByNode[n.ID]
				toCommit := make([]*entry.Entry, 0, len(produced))
				for _, e := range produced {
					// Empty-URL entries can't be tracked in failedURLs (URL is
					// the key), so they always commit. Pair with the producer-
					// side skip above so the semantics stay consistent.
					if e.URL == "" || !failedURLs[e.URL] {
						toCommit = append(toCommit, e)
					}
				}
				tc := &plugin.TaskContext{
					Name:   ex.name,
					Config: pi.Config,
					Logger: ex.logger.With("node", n.ID, "plugin", pi.Impl.Name()),
					DryRun: ex.dryRun,
				}
				if err := cp.Commit(ctx, tc, toCommit); err != nil {
					tc.Logger.Warn("commit error", "err", err)
				}
			}
		}
	}

	// Count terminal states from source entries (their State is mutable and
	// reflects all processing done to the originals).
	res.Total = len(sourceEntries)
	res.Entries = sourceEntries
	for _, e := range sourceEntries {
		switch e.State {
		case entry.Accepted:
			res.Accepted++
		case entry.Rejected:
			res.Rejected++
		case entry.Failed:
			res.Failed++
		case entry.Undecided:
			res.Undecided++
		}
	}

	res.Duration = time.Since(start)
	ex.logger.Info("pipeline done",
		"total", res.Total,
		"accepted", res.Accepted,
		"rejected", res.Rejected,
		"failed", res.Failed,
		"undecided", res.Undecided,
		"duration", res.Duration.Round(time.Millisecond),
		"dry_run", ex.dryRun,
	)
	return res, nil
}

// collectUpstream gathers entries from all upstream edges into this node.
func (ex *Executor) collectUpstream(n *dag.Node, edge map[edgeKey][]*entry.Entry) []*entry.Entry {
	if len(n.Upstreams) == 0 {
		return nil
	}
	slices := make([][]*entry.Entry, 0, len(n.Upstreams))
	for _, upID := range n.Upstreams {
		slices = append(slices, edge[edgeKey{upID, n.ID}])
	}
	return mergeAndDedup(slices...)
}

// storeOutputs distributes produced entries to each downstream consumer.
// When there are multiple consumers, all but the first get cloned copies.
func (ex *Executor) storeOutputs(n *dag.Node, produced []*entry.Entry, edge map[edgeKey][]*entry.Entry) {
	downstreams := ex.graph.Downstreams(n.ID)
	if len(downstreams) == 0 {
		return // sink node — no downstream
	}
	if len(downstreams) == 1 {
		edge[edgeKey{n.ID, downstreams[0].ID}] = produced
		return
	}
	// Fan-out: first consumer gets originals, rest get clones.
	edge[edgeKey{n.ID, downstreams[0].ID}] = produced
	for _, d := range downstreams[1:] {
		edge[edgeKey{n.ID, d.ID}] = cloneAll(produced)
	}
}

// runNode dispatches to the appropriate role interface.
func (ex *Executor) runNode(
	ctx context.Context,
	n *dag.Node,
	pi *PluginInstance,
	upstream []*entry.Entry,
) (produced []*entry.Entry, err error) {
	tc := &plugin.TaskContext{
		Name:   ex.name,
		Config: pi.Config,
		Logger: ex.logger.With("node", n.ID, "plugin", pi.Impl.Name()),
		DryRun: ex.dryRun,
	}

	role := pi.Desc.EffectiveRole()
	tc.Logger.Info("node started", "role", role, "in", len(upstream))
	nodeStart := time.Now()

	// Per-entry field validation (debug mode only).
	if ex.validateFields && role != plugin.RoleSource {
		for _, e := range upstream {
			for _, group := range pi.Desc.Requires {
				satisfied := false
				for _, f := range group {
					if _, ok := e.Fields[f]; ok {
						satisfied = true
						break
					}
				}
				if !satisfied {
					tc.Logger.Warn("entry missing required fields", "url", e.URL, "group", group)
				}
			}
		}
	}

	// Snapshot entry states before the node runs so we can log what changed.
	type snapshot struct {
		e              *entry.Entry
		stateBefore    entry.State
		consumedBefore bool
	}
	var snaps []snapshot
	if len(upstream) > 0 {
		snaps = make([]snapshot, len(upstream))
		for i, e := range upstream {
			snaps[i] = snapshot{e: e, stateBefore: e.State, consumedBefore: e.IsConsumed()}
		}
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in plugin %q: %v\n%s", pi.Impl.Name(), r, debug.Stack())
			tc.Logger.Error("plugin panic", "err", err)
		}
	}()

	switch role {
	case plugin.RoleSource:
		src, ok := pi.Impl.(plugin.SourcePlugin)
		if !ok {
			return nil, fmt.Errorf("plugin %q does not implement SourcePlugin", pi.Impl.Name())
		}
		produced, err = src.Generate(ctx, tc)

	case plugin.RoleProcessor:
		proc, ok := pi.Impl.(plugin.ProcessorPlugin)
		if !ok {
			return nil, fmt.Errorf("plugin %q does not implement ProcessorPlugin", pi.Impl.Name())
		}
		// Pre-filter upstream to the states the plugin actually acts on
		// (declared via Descriptor.InputStates, defaulting to
		// StatesAcceptedUndecided for processors). Entries in excluded states
		// bypass Process entirely and are merged back into produced unchanged
		// so they keep flowing downstream for bookkeeping. This is what
		// replaces the per-plugin "if e.IsRejected() || e.IsFailed() { continue }"
		// skip-guard at the top of every Process method.
		matching, excluded := entry.SplitByStates(upstream, effectiveInputStates(pi))
		// Strip synthetic marker entries (e.g. report_empty's "no entries"
		// placeholder) unless this plugin explicitly opts in via
		// Descriptor.AcceptsMarkers. Markers represent pipeline state, not
		// real data, and enrichment/decision processors that aren't aware
		// of them would otherwise treat the marker as a normal entry —
		// trying to e.g. look up "(no entries)" on TMDb. Filtered markers
		// merge back into produced like state-excluded entries, so they
		// keep flowing to the eventually-marker-aware downstream node.
		if !pi.Desc.AcceptsMarkers {
			var markers []*entry.Entry
			matching, markers = entry.SplitMarker(matching)
			excluded = append(excluded, markers...)
		}
		procOutput, perr := proc.Process(ctx, tc, matching)
		err = perr
		// Merge: produced = procOutput followed by the entries excluded by the
		// pre-filter. Order within each group is preserved; the plugin may
		// reorder its own slice, and excluded entries are appended at the end.
		// Downstream nodes that care about order (e.g. limit) re-sort.
		if len(excluded) == 0 {
			produced = procOutput
		} else if len(procOutput) == 0 {
			produced = excluded
		} else {
			produced = make([]*entry.Entry, 0, len(procOutput)+len(excluded))
			produced = append(produced, procOutput...)
			produced = append(produced, excluded...)
		}
		// Log state changes caused by this processor.
		for _, s := range snaps {
			if s.e.State == s.stateBefore {
				continue
			}
			switch s.e.State {
			case entry.Accepted:
				args := []any{"title", s.e.Title}
				if s.e.AcceptReason != "" {
					args = append(args, "reason", s.e.AcceptReason)
				}
				tc.Logger.Info("entry accepted", args...)
			case entry.Rejected:
				if s.stateBefore == entry.Accepted {
					tc.Logger.Info("entry accepted → rejected", "title", s.e.Title, "reason", s.e.RejectReason)
				} else {
					tc.Logger.Info("entry rejected", "title", s.e.Title, "reason", s.e.RejectReason)
				}
			case entry.Failed:
				if s.stateBefore == entry.Accepted {
					tc.Logger.Warn("entry accepted → failed", "title", s.e.Title, "reason", s.e.FailReason)
				} else {
					tc.Logger.Warn("entry failed", "title", s.e.Title, "reason", s.e.FailReason)
				}
			}
		}

	case plugin.RoleSink:
		sink, ok := pi.Impl.(plugin.SinkPlugin)
		if !ok {
			return nil, fmt.Errorf("plugin %q does not implement SinkPlugin", pi.Impl.Name())
		}
		// Pre-filter upstream to the states the sink acts on (default
		// StatesAcceptedOnly for sinks), then additionally exclude consumed
		// entries — `consumed` is orthogonal to State and an always-on filter
		// at the sink boundary regardless of the declared InputStates.
		// Excluded entries bypass Consume entirely and are merged back into
		// produced for downstream / commit-phase bookkeeping. This replaces
		// both the per-sink `entry.FilterAccepted(entries)` calls and the
		// chained-sink special case that lived here pre-#246.
		matching, excluded := entry.SplitByStates(upstream, effectiveInputStates(pi))
		matching, alsoExcluded := entry.SplitConsumed(matching)
		excluded = append(excluded, alsoExcluded...)
		// Strip marker entries unless this sink opts in. Same rationale as
		// the processor path above: a download/exec/etc. sink should never
		// act on a synthetic "no entries" placeholder, but a notify sink
		// chained off report_empty must.
		if !pi.Desc.AcceptsMarkers {
			var markers []*entry.Entry
			matching, markers = entry.SplitMarker(matching)
			excluded = append(excluded, markers...)
		}
		err = sink.Consume(ctx, tc, matching)
		if len(excluded) == 0 {
			produced = matching
		} else if len(matching) == 0 {
			produced = excluded
		} else {
			produced = make([]*entry.Entry, 0, len(matching)+len(excluded))
			produced = append(produced, matching...)
			produced = append(produced, excluded...)
		}
		// Log per-entry outcomes at every sink. Each sink only acts on its
		// pre-filtered slice, so logging at every sink (not just the terminal
		// one) reflects only the entries that sink is responsible for:
		//   deluge → "entry accepted" for the 3 it enqueued
		//   email  → "entry accepted" for the same 3 it then notified about
		for _, s := range snaps {
			if s.e.IsFailed() && s.stateBefore != entry.Failed {
				tc.Logger.Warn("entry failed", "title", s.e.Title, "reason", s.e.FailReason)
			} else if s.e.IsConsumed() && !s.consumedBefore {
				tc.Logger.Info("entry consumed", "title", s.e.Title)
			} else if s.e.IsAccepted() && !s.e.IsConsumed() {
				tc.Logger.Info("entry accepted", "title", s.e.Title)
			}
		}

	default:
		return nil, fmt.Errorf("unknown role %q for plugin %q", role, pi.Impl.Name())
	}

	dur := time.Since(nodeStart).Round(time.Millisecond)
	if err != nil {
		tc.Logger.Warn("node error", "err", err, "duration", dur)
	} else {
		args := []any{"out", len(produced), "duration", dur}
		if ports := portBreakdown(produced); ports != "" {
			args = append(args, "ports", ports)
		}
		tc.Logger.Info("node done", args...)
	}
	return produced, err
}

// portBreakdown returns a "name=N ..." summary for entries that carry a
// _route_port tag (set by the route processor). The port order follows first
// appearance in the slice. Returns "" when no entries carry a port tag.
func portBreakdown(entries []*entry.Entry) string {
	var order []string
	counts := map[string]int{}
	for _, e := range entries {
		v, ok := e.Get(entry.FieldRoutePort)
		if !ok {
			continue
		}
		name, _ := v.(string)
		if name == "" {
			continue
		}
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}
	if len(order) == 0 {
		return ""
	}
	b := make([]byte, 0, 32)
	for i, name := range order {
		if i > 0 {
			b = append(b, ' ')
		}
		b = fmt.Appendf(b, "%s=%d", name, counts[name])
	}
	return string(b)
}
