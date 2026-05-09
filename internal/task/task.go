// Package task implements the phase-ordered pipeline execution engine.
//
// Concurrency model:
//   - Input phase:  all input plugins run concurrently; results are merged (dedup by URL) before proceeding.
//   - Output phase: plugins run serially in config order; each receives only the entries still accepted at
//     that point. An output plugin may call e.Fail() to remove an entry from subsequent output plugins and
//     from the learn phase (so it is not recorded as downloaded and will be retried next run).
//   - Processing pipeline (metainfo / filter / modify): plugins run serially in config-file order, so a
//     filter can immediately follow the metainfo plugin that populates the field it inspects.
package task

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// Result summarises the outcome of a single task run.
type Result struct {
	Accepted int
	Rejected int
	Failed   int
	Total    int
	Duration time.Duration
	Entries  []*entry.Entry
}

// pluginInstance pairs a resolved plugin with its per-task config.
type pluginInstance struct {
	desc   *plugin.Descriptor
	impl   plugin.Plugin
	config map[string]any
}

// Task is a named, configured pipeline that can be Run.
type Task struct {
	name    string
	plugins []pluginInstance
	logger  *slog.Logger
	dryRun  bool
}

// New creates a Task from a name and a logger.
// Use Build (in builder.go) to construct tasks from configuration maps.
func New(name string, logger *slog.Logger) *Task {
	return &Task{name: name, logger: logger.With("task", name)}
}

// Name returns the task's name.
func (t *Task) Name() string { return t.name }

// Shutdown releases resources held by any plugin in the task that implements
// plugin.ShutdownPlugin. Call once when the task will no longer be run.
func (t *Task) Shutdown() {
	for _, pi := range t.plugins {
		if s, ok := pi.impl.(plugin.ShutdownPlugin); ok {
			s.Shutdown()
		}
	}
}

// SetDryRun enables or disables dry-run mode. In dry-run mode, the output
// and learn phases are skipped, making the run fully idempotent.
func (t *Task) SetDryRun(v bool) { t.dryRun = v }

func (t *Task) addPlugin(pi pluginInstance) {
	t.plugins = append(t.plugins, pi)
}

// Run executes the task and returns a Result. Input plugins run concurrently.
// Metainfo, filter, and modify plugins run serially in config-file order.
// Output plugins run serially in config order; each receives only entries still
// accepted at that point, so an output that fails an entry prevents subsequent
// outputs from seeing it. Panics inside any plugin call are caught and converted
// to logged errors; the task continues unless the context is cancelled.
func (t *Task) Run(ctx context.Context) (*Result, error) {
	start := time.Now()
	if t.dryRun {
		t.logger.Info("task started (DRY RUN)")
	} else {
		t.logger.Info("task started")
	}

	tc := func(pi pluginInstance) *plugin.TaskContext {
		return &plugin.TaskContext{
			Name:   t.name,
			Config: pi.config,
			Logger: t.logger.With("phase", pi.impl.Phase(), "plugin", pi.impl.Name()),
		}
	}

	// --- Input phase (concurrent) ---
	t.logger.Info("phase started", "phase", "input")
	phaseStart := time.Now()
	entries := t.runInputPhase(ctx, t.logger, tc)
	t.logger.Info("phase done", "phase", "input", "entries", len(entries), "duration", time.Since(phaseStart).Round(time.Millisecond))

	// --- Processing pipeline: metainfo / filter / modify in config order ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, pi := range t.processingPlugins() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		plog := t.logger.With("phase", pi.impl.Phase(), "plugin", pi.impl.Name())
		pluginStart := time.Now()
		switch pi.impl.Phase() {
		case plugin.PhaseMetainfo:
			n := len(entries) - countRejected(entries)
			plog.Info("plugin started", "in", n)
			if batch, ok := pi.impl.(plugin.BatchMetainfoPlugin); ok {
				live := make([]*entry.Entry, 0, n)
				for _, e := range entries {
					if !e.IsRejected() && !e.IsFailed() {
						live = append(live, e)
					}
				}
				if err := safeRunAnnotateBatch(ctx, batch, tc(pi), live); err != nil {
					plog.Warn("metainfo plugin error", "err", err)
				}
			} else if meta, ok := pi.impl.(plugin.MetainfoPlugin); ok {
				for _, e := range entries {
					if e.IsRejected() || e.IsFailed() {
						continue
					}
					if err := safeRunAnnotate(ctx, meta, tc(pi), e); err != nil {
						plog.Warn("metainfo plugin error", "entry", e.Title, "err", err)
					}
				}
			}
			plog.Info("plugin done", "out", n, "duration", time.Since(pluginStart).Round(time.Millisecond))

		case plugin.PhaseFilter:
			snap := snapshotStates(entries)
			inA := len(entries) - countRejected(entries)
			plog.Info("plugin started", "in", inA)
			if batch, ok := pi.impl.(plugin.BatchFilterPlugin); ok {
				if err := safeRunFilterBatch(ctx, batch, tc(pi), entries); err != nil {
					plog.Warn("filter plugin error", "err", err)
				}
				for _, e := range entries {
					if prev := snap[e]; e.State != prev {
						logStateChange(plog, prev, e.State, e.Title, e.RejectReason+e.FailReason)
					}
				}
			} else if flt, ok := pi.impl.(plugin.FilterPlugin); ok {
				for _, e := range entries {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					if e.IsRejected() || e.IsFailed() {
						continue
					}
					prev := e.State
					if err := safeRunFilter(ctx, flt, tc(pi), e); err != nil {
						plog.Warn("filter plugin error", "entry", e.Title, "err", err)
					}
					if e.State != prev {
						logStateChange(plog, prev, e.State, e.Title, e.RejectReason+e.FailReason)
					}
				}
			}
			accepted, rejected, overridden := filterDelta(snap, entries)
			outA := len(entries) - countRejected(entries)
			args := []any{"in", inA, "out", outA}
			if accepted > 0   { args = append(args, "accepted", accepted) }
			if rejected > 0   { args = append(args, "rejected", rejected) }
			if overridden > 0 { args = append(args, "overridden", overridden) }
			args = append(args, "duration", time.Since(pluginStart).Round(time.Millisecond))
			plog.Info("plugin done", args...)

		case plugin.PhaseModify:
			mod, ok := pi.impl.(plugin.ModifyPlugin)
			if !ok {
				continue
			}
			n := len(entries) - countRejected(entries)
			plog.Info("plugin started", "in", n)
			for _, e := range entries {
				if e.IsRejected() || e.IsFailed() {
					continue
				}
				if err := safeRunModify(ctx, mod, tc(pi), e); err != nil {
					plog.Warn("modify plugin error", "entry", e.Title, "err", err)
				}
			}
			plog.Info("plugin done", "out", n, "duration", time.Since(pluginStart).Round(time.Millisecond))
		}
	}

	// --- Episode deduplication (automatic, post-processing) ---
	// When multiple accepted entries share the same series + episode ID, keep
	// the best one (seed tier → resolution → seeds) and reject the rest.
	// Entries without series_name or series_episode_id are not affected.
	for _, e := range entries {
		if e.IsUndecided() {
			t.logger.Info("entry undecided", "entry", e.Title)
		}
	}
	deduplicate(entries, t.logger)

	// --- Output phase (serial — each plugin receives only entries still accepted at that point) ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.logger.Info("phase started", "phase", "output", "accepted", len(filterByState(entries, entry.Accepted)))
	phaseStart = time.Now()
	if t.dryRun {
		t.logger.Info("skipping output phase (dry run)")
	} else {
		t.runOutputPhase(ctx, t.logger, tc, entries)
	}
	t.logger.Info("phase done", "phase", "output", "duration", time.Since(phaseStart).Round(time.Millisecond))

	// --- Learn phase (serial — writes to shared store) ---
	t.logger.Info("phase started", "phase", "learn")
	phaseStart = time.Now()
	if t.dryRun {
		t.logger.Info("skipping learn phase (dry run)")
		t.logger.Info("phase done", "phase", "learn", "duration", time.Since(phaseStart).Round(time.Millisecond))
		return buildResult(entries, time.Since(start)), nil
	}
	// All plugins that implement LearnPlugin receive the entries here.
	for _, pi := range t.plugins {
		lrn, ok := pi.impl.(plugin.LearnPlugin)
		if !ok {
			continue
		}
		plog := t.logger.With("phase", "learn", "plugin", pi.impl.Name())
		plog.Info("plugin started", "in", len(entries))
		pluginStart := time.Now()
		if err := safeRunLearn(ctx, lrn, tc(pi), entries); err != nil {
			plog.Warn("learn plugin error", "err", err)
		}
		plog.Info("plugin done", "out", len(entries), "duration", time.Since(pluginStart).Round(time.Millisecond))
	}
	t.logger.Info("phase done", "phase", "learn", "duration", time.Since(phaseStart).Round(time.Millisecond))

	return buildResult(entries, time.Since(start)), nil
}

// runInputPhase runs all input plugins concurrently and merges their results,
// deduplicating by URL. Order within the merged slice is non-deterministic
// across plugins but stable within each plugin's output.
func (t *Task) runInputPhase(ctx context.Context, log *slog.Logger, tc func(pluginInstance) *plugin.TaskContext) []*entry.Entry {
	inputs := t.pluginsForPhase(plugin.PhaseInput)
	if len(inputs) == 0 {
		return nil
	}

	type result struct {
		entries []*entry.Entry
		err     error
		name    string
	}
	ch := make(chan result, len(inputs))

	var wg sync.WaitGroup
	for _, pi := range inputs {
		inp, ok := pi.impl.(plugin.InputPlugin)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(inp plugin.InputPlugin, tctx *plugin.TaskContext) {
			defer wg.Done()
			plog := log.With("phase", plugin.PhaseInput, "plugin", inp.Name())
			plog.Info("plugin started", "in", 0)
			pluginStart := time.Now()
			produced, err := safeRunInput(ctx, inp, tctx)
			if err == nil {
				plog.Info("plugin done", "out", len(produced), "duration", time.Since(pluginStart).Round(time.Millisecond))
			}
			ch <- result{entries: produced, err: err, name: inp.Name()}
		}(inp, tc(pi))
	}

	// Close channel once all goroutines finish.
	go func() {
		wg.Wait()
		close(ch)
	}()

	seen := map[string]bool{}
	var entries []*entry.Entry
	for r := range ch {
		if r.err != nil {
			log.Warn("input plugin error", "phase", plugin.PhaseInput, "plugin", r.name, "err", r.err)
			continue
		}
		for _, e := range r.entries {
			e.Task = t.name
			if seen[e.URL] {
				continue
			}
			seen[e.URL] = true
			entries = append(entries, e)
		}
	}
	return entries
}

// runOutputPhase runs output plugins serially in config order. Each plugin
// receives only the entries still accepted at the time it runs. A plugin that
// calls e.Fail() removes that entry from subsequent output plugins and from the
// learn phase, so it will not be recorded as downloaded and will be retried.
func (t *Task) runOutputPhase(ctx context.Context, log *slog.Logger, tc func(pluginInstance) *plugin.TaskContext, entries []*entry.Entry) {
	for _, pi := range t.pluginsForPhase(plugin.PhaseOutput) {
		out, ok := pi.impl.(plugin.OutputPlugin)
		if !ok {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		accepted := filterByState(entries, entry.Accepted)
		plog := log.With("phase", plugin.PhaseOutput, "plugin", out.Name())
		plog.Info("plugin started", "in", len(accepted))
		pluginStart := time.Now()
		snap := snapshotStates(accepted)
		if err := safeRunOutput(ctx, out, tc(pi), accepted); err != nil {
			plog.Warn("output plugin error", "err", err)
		}
		failed := 0
		for _, e := range accepted {
			if prev := snap[e]; e.State != prev && e.IsFailed() {
				plog.Warn("entry failed", "entry", e.Title, "reason", e.FailReason)
				failed++
			}
		}
		args := []any{"in", len(accepted), "out", len(accepted) - failed}
		if failed > 0 {
			args = append(args, "failed", failed)
		}
		args = append(args, "duration", time.Since(pluginStart).Round(time.Millisecond))
		plog.Info("plugin done", args...)
	}
}

func (t *Task) pluginsForPhase(p plugin.Phase) []pluginInstance {
	var out []pluginInstance
	for _, pi := range t.plugins {
		if pi.impl.Phase() == p {
			out = append(out, pi)
		}
	}
	return out
}

// processingPlugins returns all metainfo, filter, and modify plugins in config order.
func (t *Task) processingPlugins() []pluginInstance {
	var out []pluginInstance
	for _, pi := range t.plugins {
		switch pi.impl.Phase() {
		case plugin.PhaseMetainfo, plugin.PhaseFilter, plugin.PhaseModify:
			out = append(out, pi)
		}
	}
	return out
}

// logStateChange logs a filter state transition. When an already-accepted
// entry is rejected or failed it is logged at WARN with the prior state so
// the override is clearly visible.
func logStateChange(log *slog.Logger, prev, next entry.State, title, reason string) {
	override := prev == entry.Accepted && (next == entry.Rejected || next == entry.Failed)
	switch next {
	case entry.Accepted:
		log.Info("entry accepted", "entry", title)
	case entry.Rejected:
		if override {
			log.Info("entry accepted then rejected", "entry", title, "reason", reason)
		} else {
			log.Info("entry rejected", "entry", title, "reason", reason)
		}
	case entry.Failed:
		if override {
			log.Info("entry accepted then failed", "entry", title, "reason", reason)
		} else {
			log.Warn("entry failed", "entry", title, "reason", reason)
		}
	}
}

// snapshotStates captures the current state of every entry.
func snapshotStates(entries []*entry.Entry) map[*entry.Entry]entry.State {
	m := make(map[*entry.Entry]entry.State, len(entries))
	for _, e := range entries {
		m[e] = e.State
	}
	return m
}

// filterDelta counts state transitions since the snapshot.
// Returns (accepted, rejected, overridden) where overridden counts
// entries that moved from Accepted → Rejected/Failed.
func filterDelta(before map[*entry.Entry]entry.State, entries []*entry.Entry) (accepted, rejected, overridden int) {
	for _, e := range entries {
		prev := before[e]
		if prev == e.State {
			continue
		}
		switch {
		case e.IsAccepted():
			accepted++
		case e.IsRejected() || e.IsFailed():
			rejected++
			if prev == entry.Accepted {
				overridden++
			}
		}
	}
	return
}

func countRejected(entries []*entry.Entry) int {
	n := 0
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			n++
		}
	}
	return n
}

func filterByState(entries []*entry.Entry, s entry.State) []*entry.Entry {
	var out []*entry.Entry
	for _, e := range entries {
		if e.State == s {
			out = append(out, e)
		}
	}
	return out
}

func buildResult(entries []*entry.Entry, dur time.Duration) *Result {
	r := &Result{Total: len(entries), Duration: dur, Entries: entries}
	for _, e := range entries {
		switch e.State {
		case entry.Accepted:
			r.Accepted++
		case entry.Rejected:
			r.Rejected++
		case entry.Failed:
			r.Failed++
		}
	}
	return r
}

// --- safe wrappers that convert panics to errors ---

// panicErr converts a recovered panic value into an error that includes
// the full stack trace for easier debugging in production.
func panicErr(pluginName string, r any) error {
	return fmt.Errorf("panic in plugin %q: %v\n%s", pluginName, r, debug.Stack())
}

func safeRunInput(ctx context.Context, p plugin.InputPlugin, tc *plugin.TaskContext) (out []*entry.Entry, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr(p.Name(), r)
		}
	}()
	return p.Run(ctx, tc)
}

func safeRunAnnotate(ctx context.Context, p plugin.MetainfoPlugin, tc *plugin.TaskContext, e *entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr(p.Name(), r)
		}
	}()
	return p.Annotate(ctx, tc, e)
}

func safeRunAnnotateBatch(ctx context.Context, p plugin.BatchMetainfoPlugin, tc *plugin.TaskContext, entries []*entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr(p.Name(), r)
		}
	}()
	return p.AnnotateBatch(ctx, tc, entries)
}

func safeRunFilterBatch(ctx context.Context, p plugin.BatchFilterPlugin, tc *plugin.TaskContext, entries []*entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr(p.Name(), r)
		}
	}()
	return p.FilterBatch(ctx, tc, entries)
}

func safeRunFilter(ctx context.Context, p plugin.FilterPlugin, tc *plugin.TaskContext, e *entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr(p.Name(), r)
		}
	}()
	return p.Filter(ctx, tc, e)
}

func safeRunModify(ctx context.Context, p plugin.ModifyPlugin, tc *plugin.TaskContext, e *entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr(p.Name(), r)
		}
	}()
	return p.Modify(ctx, tc, e)
}

func safeRunOutput(ctx context.Context, p plugin.OutputPlugin, tc *plugin.TaskContext, entries []*entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr(p.Name(), r)
		}
	}()
	return p.Output(ctx, tc, entries)
}

func safeRunLearn(ctx context.Context, p plugin.LearnPlugin, tc *plugin.TaskContext, entries []*entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr(p.Name(), r)
		}
	}()
	return p.Learn(ctx, tc, entries)
}
