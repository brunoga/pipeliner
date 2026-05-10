// Package executor runs a DAG pipeline graph, feeding entries from source
// nodes through processor nodes to sink nodes.
//
// Execution order:
//  1. Compute topological layers via dag.Graph.Layers().
//  2. Execute layers in order; within a layer all nodes run serially.
//  3. For each node, collect entries from all upstream nodes (merge + dedup
//     by URL), adapt the plugin to its role interface, then invoke it.
//  4. When a node's output fans out to more than one downstream consumer,
//     entries are cloned per consumer so each branch has independent copies.
//
// Legacy plugins (PhaseInput, PhaseMetainfo, PhaseFilter, PhaseModify,
// PhaseOutput, PhaseLearn) are wrapped by adapt.go so they participate in
// DAG pipelines without modification.
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

// edgeKey identifies the directed edge from a producer node to a consumer node.
// Entries are stored per-edge so fan-out branches each get their own slice.
type edgeKey struct{ from, to dag.NodeID }

// Executor runs a DAG pipeline.
type Executor struct {
	name    string
	graph   *dag.Graph
	plugins map[dag.NodeID]*PluginInstance
	logger  *slog.Logger
	dryRun  bool
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

			if pi.Desc.EffectiveRole() == plugin.RoleSource {
				sourceEntries = append(sourceEntries, produced...)
			}

			ex.storeOutputs(n, produced, edge)
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
		}
	}

	res.Duration = time.Since(start)
	ex.logger.Info("pipeline done",
		"total", res.Total,
		"accepted", res.Accepted,
		"rejected", res.Rejected,
		"failed", res.Failed,
		"duration", res.Duration.Round(time.Millisecond),
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

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in plugin %q: %v\n%s", pi.Impl.Name(), r, debug.Stack())
			tc.Logger.Error("plugin panic", "err", err)
		}
	}()

	switch role {
	case plugin.RoleSource:
		src, adapterErr := asSource(pi.Impl)
		if adapterErr != nil {
			return nil, adapterErr
		}
		produced, err = src.Generate(ctx, tc)

	case plugin.RoleProcessor:
		proc, adapterErr := asProcessor(pi.Impl)
		if adapterErr != nil {
			return nil, adapterErr
		}
		produced, err = proc.Process(ctx, tc, upstream)

	case plugin.RoleSink:
		sink, adapterErr := asSink(pi.Impl)
		if adapterErr != nil {
			return nil, adapterErr
		}
		err = sink.Consume(ctx, tc, upstream)
		produced = nil

	default:
		return nil, fmt.Errorf("unknown role %q for plugin %q", role, pi.Impl.Name())
	}

	dur := time.Since(nodeStart).Round(time.Millisecond)
	if err != nil {
		tc.Logger.Warn("node error", "err", err, "duration", dur)
	} else {
		tc.Logger.Info("node done", "out", len(produced), "duration", dur)
	}
	return produced, err
}
