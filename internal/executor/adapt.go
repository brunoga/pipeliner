package executor

// adapt.go wraps legacy phase-based plugin implementations into the new
// SourcePlugin / ProcessorPlugin / SinkPlugin interfaces used by the executor.
// This lets every existing plugin participate in DAG pipelines without any
// changes to its own package.

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// asSource returns a SourcePlugin for the given plugin implementation.
// Supports: SourcePlugin (native), InputPlugin (legacy).
func asSource(p plugin.Plugin) (plugin.SourcePlugin, error) {
	if s, ok := p.(plugin.SourcePlugin); ok {
		return s, nil
	}
	if inp, ok := p.(plugin.InputPlugin); ok {
		return &inputAdapter{inner: inp}, nil
	}
	return nil, fmt.Errorf("plugin %q does not implement SourcePlugin or InputPlugin", p.Name())
}

// asProcessor returns a ProcessorPlugin for the given plugin implementation.
// Supports: ProcessorPlugin (native), BatchMetainfoPlugin, MetainfoPlugin,
// BatchFilterPlugin, FilterPlugin, ModifyPlugin (all legacy).
func asProcessor(p plugin.Plugin) (plugin.ProcessorPlugin, error) {
	if proc, ok := p.(plugin.ProcessorPlugin); ok {
		return proc, nil
	}
	switch p.Phase() {
	case plugin.PhaseMetainfo:
		if batch, ok := p.(plugin.BatchMetainfoPlugin); ok {
			return &batchMetainfoAdapter{inner: batch}, nil
		}
		if meta, ok := p.(plugin.MetainfoPlugin); ok {
			return &metainfoAdapter{inner: meta}, nil
		}
	case plugin.PhaseFilter:
		if batch, ok := p.(plugin.BatchFilterPlugin); ok {
			return &batchFilterAdapter{inner: batch}, nil
		}
		if flt, ok := p.(plugin.FilterPlugin); ok {
			return &filterAdapter{inner: flt}, nil
		}
	case plugin.PhaseModify:
		if mod, ok := p.(plugin.ModifyPlugin); ok {
			return &modifyAdapter{inner: mod}, nil
		}
	}
	return nil, fmt.Errorf("plugin %q does not implement ProcessorPlugin or any legacy processor interface", p.Name())
}

// asSink returns a SinkPlugin for the given plugin implementation.
// Supports: SinkPlugin (native), OutputPlugin, LearnPlugin (legacy).
func asSink(p plugin.Plugin) (plugin.SinkPlugin, error) {
	if s, ok := p.(plugin.SinkPlugin); ok {
		return s, nil
	}
	switch p.Phase() {
	case plugin.PhaseOutput:
		if out, ok := p.(plugin.OutputPlugin); ok {
			return &outputAdapter{inner: out}, nil
		}
	case plugin.PhaseLearn:
		if lrn, ok := p.(plugin.LearnPlugin); ok {
			return &learnAdapter{inner: lrn}, nil
		}
	}
	return nil, fmt.Errorf("plugin %q does not implement SinkPlugin or any legacy sink interface", p.Name())
}

// --- legacy adapters ---

type inputAdapter struct{ inner plugin.InputPlugin }

func (a *inputAdapter) Name() string  { return a.inner.Name() }
func (a *inputAdapter) Phase() plugin.Phase { return a.inner.Phase() }
func (a *inputAdapter) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return a.inner.Run(ctx, tc)
}

type metainfoAdapter struct{ inner plugin.MetainfoPlugin }

func (a *metainfoAdapter) Name() string  { return a.inner.Name() }
func (a *metainfoAdapter) Phase() plugin.Phase { return a.inner.Phase() }
func (a *metainfoAdapter) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := a.inner.Annotate(ctx, tc, e); err != nil {
			tc.Logger.Warn("metainfo plugin error", "entry", e.Title, "err", err)
		}
	}
	return entries, nil
}

type batchMetainfoAdapter struct{ inner plugin.BatchMetainfoPlugin }

func (a *batchMetainfoAdapter) Name() string  { return a.inner.Name() }
func (a *batchMetainfoAdapter) Phase() plugin.Phase { return a.inner.Phase() }
func (a *batchMetainfoAdapter) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	live := make([]*entry.Entry, 0, len(entries))
	for _, e := range entries {
		if !e.IsRejected() && !e.IsFailed() {
			live = append(live, e)
		}
	}
	if err := a.inner.AnnotateBatch(ctx, tc, live); err != nil {
		tc.Logger.Warn("metainfo plugin error", "err", err)
	}
	return entries, nil
}

type filterAdapter struct{ inner plugin.FilterPlugin }

func (a *filterAdapter) Name() string  { return a.inner.Name() }
func (a *filterAdapter) Phase() plugin.Phase { return a.inner.Phase() }
func (a *filterAdapter) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := a.inner.Filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("filter plugin error", "entry", e.Title, "err", err)
		}
	}
	return passThrough(entries), nil
}

type batchFilterAdapter struct{ inner plugin.BatchFilterPlugin }

func (a *batchFilterAdapter) Name() string  { return a.inner.Name() }
func (a *batchFilterAdapter) Phase() plugin.Phase { return a.inner.Phase() }
func (a *batchFilterAdapter) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	if err := a.inner.FilterBatch(ctx, tc, entries); err != nil {
		tc.Logger.Warn("filter plugin error", "err", err)
	}
	return passThrough(entries), nil
}

type modifyAdapter struct{ inner plugin.ModifyPlugin }

func (a *modifyAdapter) Name() string  { return a.inner.Name() }
func (a *modifyAdapter) Phase() plugin.Phase { return a.inner.Phase() }
func (a *modifyAdapter) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := a.inner.Modify(ctx, tc, e); err != nil {
			tc.Logger.Warn("modify plugin error", "entry", e.Title, "err", err)
		}
	}
	return entries, nil
}

type outputAdapter struct{ inner plugin.OutputPlugin }

func (a *outputAdapter) Name() string  { return a.inner.Name() }
func (a *outputAdapter) Phase() plugin.Phase { return a.inner.Phase() }
func (a *outputAdapter) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	accepted := filterAccepted(entries)
	return a.inner.Output(ctx, tc, accepted)
}

type learnAdapter struct{ inner plugin.LearnPlugin }

func (a *learnAdapter) Name() string  { return a.inner.Name() }
func (a *learnAdapter) Phase() plugin.Phase { return a.inner.Phase() }
func (a *learnAdapter) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	accepted := filterAccepted(entries)
	return a.inner.Learn(ctx, tc, accepted)
}

// passThrough returns entries that are not rejected/failed, preserving the
// pointer identity of non-filtered entries (no allocation for the common path
// where nothing is filtered).
func passThrough(entries []*entry.Entry) []*entry.Entry {
	allPass := true
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			allPass = false
			break
		}
	}
	if allPass {
		return entries
	}
	out := make([]*entry.Entry, 0, len(entries))
	for _, e := range entries {
		if !e.IsRejected() && !e.IsFailed() {
			out = append(out, e)
		}
	}
	return out
}

func filterAccepted(entries []*entry.Entry) []*entry.Entry {
	out := make([]*entry.Entry, 0, len(entries))
	for _, e := range entries {
		if e.IsAccepted() {
			out = append(out, e)
		}
	}
	return out
}
