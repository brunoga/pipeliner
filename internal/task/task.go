// Package task implements the phase-ordered pipeline execution engine.
//
// Concurrency model:
//   - Input phase:  all input plugins run concurrently; results are merged (dedup by URL) before proceeding.
//   - Output phase: all output plugins run concurrently; they receive the same read-only slice of accepted entries.
//   - All other phases run serially to preserve ordering guarantees (series tracking, field dependencies).
package task

import (
	"context"
	"fmt"
	"log/slog"
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

// SetDryRun enables or disables dry-run mode. In dry-run mode, the output
// phase is skipped.
func (t *Task) SetDryRun(v bool) { t.dryRun = v }

func (t *Task) addPlugin(pi pluginInstance) {
	t.plugins = append(t.plugins, pi)
}

// Run executes the task through all phases in order and returns a Result.
//
// Input and output plugins run concurrently within their phase. All other
// phases are serial. Panics inside any plugin call are caught and converted
// to logged errors; the task continues with remaining plugins unless the
// context is cancelled.
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
	entries := t.runInputPhase(ctx, t.logger, tc)
	t.logger.Info("input phase done", "entries", len(entries))

	// --- Metainfo phase (batch or serial per entry) ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, pi := range t.pluginsForPhase(plugin.PhaseMetainfo) {
		if batch, ok := pi.impl.(plugin.BatchMetainfoPlugin); ok {
			if err := safeRunAnnotateBatch(ctx, batch, tc(pi), entries); err != nil {
				t.logger.Warn("metainfo plugin error", "phase", pi.impl.Phase(), "plugin", pi.impl.Name(), "err", err)
			}
			continue
		}
		meta, ok := pi.impl.(plugin.MetainfoPlugin)
		if !ok {
			continue
		}
		for _, e := range entries {
			if err := safeRunAnnotate(ctx, meta, tc(pi), e); err != nil {
				t.logger.Warn("metainfo plugin error", "phase", pi.impl.Phase(), "plugin", pi.impl.Name(), "entry", e.Title, "err", err)
			}
		}
	}

	// --- Filter phase (serial per entry — ordering matters for series tracking) ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, pi := range t.pluginsForPhase(plugin.PhaseFilter) {
		flt, ok := pi.impl.(plugin.FilterPlugin)
		if !ok {
			continue
		}
		for _, e := range entries {
			if e.IsRejected() || e.IsFailed() {
				continue // already decided; skip further filtering
			}
			prev := e.State
			if err := safeRunFilter(ctx, flt, tc(pi), e); err != nil {
				t.logger.Warn("filter plugin error", "phase", pi.impl.Phase(), "plugin", pi.impl.Name(), "entry", e.Title, "err", err)
			}
			if e.State != prev {
				switch e.State {
				case entry.Accepted:
					t.logger.Info("entry accepted", "plugin", pi.impl.Name(), "entry", e.Title)
				case entry.Rejected:
					t.logger.Info("entry rejected", "plugin", pi.impl.Name(), "entry", e.Title, "reason", e.RejectReason)
				case entry.Failed:
					t.logger.Warn("entry failed", "plugin", pi.impl.Name(), "entry", e.Title, "reason", e.FailReason)
				}
			}
		}
	}
	for _, e := range entries {
		if e.IsUndecided() {
			t.logger.Info("entry undecided", "entry", e.Title)
		}
	}

	// --- Modify phase (serial per entry — plugins may depend on each other's writes) ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, pi := range t.pluginsForPhase(plugin.PhaseModify) {
		mod, ok := pi.impl.(plugin.ModifyPlugin)
		if !ok {
			continue
		}
		for _, e := range entries {
			if e.IsRejected() || e.IsFailed() {
				continue
			}
			if err := safeRunModify(ctx, mod, tc(pi), e); err != nil {
				t.logger.Warn("modify plugin error", "phase", pi.impl.Phase(), "plugin", pi.impl.Name(), "entry", e.Title, "err", err)
			}
		}
	}

	// --- Output phase (concurrent — independent external systems, entries are read-only) ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accepted := filterByState(entries, entry.Accepted)
	if t.dryRun {
		t.logger.Info("skipping output phase (dry run)", "accepted", len(accepted))
	} else {
		t.runOutputPhase(ctx, t.logger, tc, accepted)
	}

	// --- Learn phase (serial — writes to shared store) ---
	// All plugins that implement LearnPlugin receive the entries here.
	for _, pi := range t.plugins {
		lrn, ok := pi.impl.(plugin.LearnPlugin)
		if !ok {
			continue
		}
		if err := safeRunLearn(ctx, lrn, tc(pi), entries); err != nil {
			t.logger.Warn("learn plugin error", "phase", pi.impl.Phase(), "plugin", pi.impl.Name(), "err", err)
		}
	}

	result := buildResult(entries, time.Since(start))
	t.logger.Info("task finished",
		"accepted", result.Accepted,
		"rejected", result.Rejected,
		"failed", result.Failed,
		"duration", result.Duration,
	)
	return result, nil
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
			produced, err := safeRunInput(ctx, inp, tctx)
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

// runOutputPhase runs all output plugins concurrently, each receiving the same
// slice of accepted entries. Errors are logged; the task continues regardless.
func (t *Task) runOutputPhase(ctx context.Context, log *slog.Logger, tc func(pluginInstance) *plugin.TaskContext, accepted []*entry.Entry) {
	outputs := t.pluginsForPhase(plugin.PhaseOutput)
	if len(outputs) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, pi := range outputs {
		out, ok := pi.impl.(plugin.OutputPlugin)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(out plugin.OutputPlugin, tctx *plugin.TaskContext) {
			defer wg.Done()
			if err := safeRunOutput(ctx, out, tctx, accepted); err != nil {
				log.Warn("output plugin error", "phase", plugin.PhaseOutput, "plugin", out.Name(), "err", err)
			}
		}(out, tc(pi))
	}
	wg.Wait()
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

func safeRunInput(ctx context.Context, p plugin.InputPlugin, tc *plugin.TaskContext) (out []*entry.Entry, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in input plugin %q: %v", p.Name(), r)
		}
	}()
	return p.Run(ctx, tc)
}

func safeRunAnnotate(ctx context.Context, p plugin.MetainfoPlugin, tc *plugin.TaskContext, e *entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in metainfo plugin %q: %v", p.Name(), r)
		}
	}()
	return p.Annotate(ctx, tc, e)
}

func safeRunAnnotateBatch(ctx context.Context, p plugin.BatchMetainfoPlugin, tc *plugin.TaskContext, entries []*entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in metainfo plugin %q: %v", p.Name(), r)
		}
	}()
	return p.AnnotateBatch(ctx, tc, entries)
}

func safeRunFilter(ctx context.Context, p plugin.FilterPlugin, tc *plugin.TaskContext, e *entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in filter plugin %q: %v", p.Name(), r)
		}
	}()
	return p.Filter(ctx, tc, e)
}

func safeRunModify(ctx context.Context, p plugin.ModifyPlugin, tc *plugin.TaskContext, e *entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in modify plugin %q: %v", p.Name(), r)
		}
	}()
	return p.Modify(ctx, tc, e)
}

func safeRunOutput(ctx context.Context, p plugin.OutputPlugin, tc *plugin.TaskContext, entries []*entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in output plugin %q: %v", p.Name(), r)
		}
	}()
	return p.Output(ctx, tc, entries)
}

func safeRunLearn(ctx context.Context, p plugin.LearnPlugin, tc *plugin.TaskContext, entries []*entry.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in learn plugin %q: %v", p.Name(), r)
		}
	}()
	return p.Learn(ctx, tc, entries)
}
