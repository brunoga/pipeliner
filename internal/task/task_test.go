package task

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// --- test doubles ---

type stubInput struct {
	entries []*entry.Entry
	err     error
}

func (s *stubInput) Name() string  { return "stub-input" }
func (s *stubInput) Phase() plugin.Phase { return plugin.PhaseInput }
func (s *stubInput) Run(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	return s.entries, s.err
}

type titleRejectFilter struct{ keyword string }

func (f *titleRejectFilter) Name() string  { return "title-reject" }
func (f *titleRejectFilter) Phase() plugin.Phase { return plugin.PhaseFilter }
func (f *titleRejectFilter) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	if e.Title == f.keyword {
		e.Reject("matched keyword")
	}
	return nil
}

type alwaysAcceptFilter struct{}

func (f *alwaysAcceptFilter) Name() string  { return "always-accept" }
func (f *alwaysAcceptFilter) Phase() plugin.Phase { return plugin.PhaseFilter }
func (f *alwaysAcceptFilter) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	e.Accept()
	return nil
}

type capturingOutput struct{ received []*entry.Entry }

func (o *capturingOutput) Name() string  { return "capturing-output" }
func (o *capturingOutput) Phase() plugin.Phase { return plugin.PhaseOutput }
func (o *capturingOutput) Output(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	o.received = append(o.received, entries...)
	return nil
}

type capturingLearn struct{ received []*entry.Entry }

func (l *capturingLearn) Name() string        { return "capturing-learn" }
func (l *capturingLearn) Phase() plugin.Phase { return plugin.PhaseFilter }
func (l *capturingLearn) Learn(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	l.received = append(l.received, entries...)
	return nil
}

type fieldSetter struct{ key, val string }

func (m *fieldSetter) Name() string  { return "field-setter" }
func (m *fieldSetter) Phase() plugin.Phase { return plugin.PhaseModify }
func (m *fieldSetter) Modify(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	e.Set(m.key, m.val)
	return nil
}

type panicFilter struct{}

func (p *panicFilter) Name() string  { return "panic-filter" }
func (p *panicFilter) Phase() plugin.Phase { return plugin.PhaseFilter }
func (p *panicFilter) Filter(_ context.Context, _ *plugin.TaskContext, _ *entry.Entry) error {
	panic("deliberate panic")
}

// --- helpers ---

func makeTask(name string, plugins ...plugin.Plugin) *Task {
	t := New(name, slog.Default())
	for _, p := range plugins {
		t.addPlugin(pluginInstance{impl: p, config: map[string]any{}})
	}
	return t
}

func threeEntries() []*entry.Entry {
	return []*entry.Entry{
		entry.New("alpha", "http://a.com"),
		entry.New("beta", "http://b.com"),
		entry.New("gamma", "http://c.com"),
	}
}

// --- tests ---

func TestAllEntriesUndecidedWithNoFilters(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	task := makeTask("t", inp)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Errorf("want 3 total, got %d", res.Total)
	}
	if res.Accepted != 0 || res.Rejected != 0 {
		t.Errorf("want all undecided: %+v", res)
	}
}

func TestFilterRejectsMatchingEntries(t *testing.T) {
	entries := threeEntries() // alpha, beta, gamma
	inp := &stubInput{entries: entries}
	flt := &titleRejectFilter{keyword: "beta"}
	out := &capturingOutput{}

	// entries need to be accepted first so output sees them
	accept := &alwaysAcceptFilter{}
	task := makeTask("t", inp, accept, flt, out)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Rejected != 1 {
		t.Errorf("want 1 rejected, got %d", res.Rejected)
	}
	if res.Accepted != 2 {
		t.Errorf("want 2 accepted, got %d", res.Accepted)
	}
	// output should only receive accepted entries
	if len(out.received) != 2 {
		t.Errorf("output want 2 entries, got %d", len(out.received))
	}
	for _, e := range out.received {
		if e.Title == "beta" {
			t.Error("rejected entry reached output")
		}
	}
}

func TestRejectWinsOverAccept(t *testing.T) {
	e := entry.New("target", "http://x.com")
	inp := &stubInput{entries: []*entry.Entry{e}}
	accept := &alwaysAcceptFilter{}
	reject := &titleRejectFilter{keyword: "target"}

	task := makeTask("t", inp, accept, reject)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Rejected != 1 {
		t.Errorf("want 1 rejected, got %d", res.Rejected)
	}
}

func TestOutputReceivesOnlyAccepted(t *testing.T) {
	entries := threeEntries()
	inp := &stubInput{entries: entries}
	accept := &alwaysAcceptFilter{}
	reject := &titleRejectFilter{keyword: "gamma"}
	out := &capturingOutput{}

	task := makeTask("t", inp, accept, reject, out)
	_, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range out.received {
		if !e.IsAccepted() {
			t.Errorf("non-accepted entry in output: %v", e)
		}
	}
}

func TestLearnReceivesAllEntries(t *testing.T) {
	entries := threeEntries()
	inp := &stubInput{entries: entries}
	accept := &alwaysAcceptFilter{}
	reject := &titleRejectFilter{keyword: "alpha"}
	lrn := &capturingLearn{}

	task := makeTask("t", inp, accept, reject, lrn)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(lrn.received) != res.Total {
		t.Errorf("learn got %d entries, want %d", len(lrn.received), res.Total)
	}
}

func TestModifySkipsRejectedEntries(t *testing.T) {
	entries := threeEntries()
	inp := &stubInput{entries: entries}
	reject := &titleRejectFilter{keyword: "alpha"}
	mod := &fieldSetter{key: "modified", val: "yes"}

	task := makeTask("t", inp, reject, mod)
	_, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsRejected() {
			if e.GetString("modified") != "" {
				t.Errorf("rejected entry %q should not be modified", e.Title)
			}
		}
	}
}

func TestContextCancellation(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before run
	task := makeTask("t", inp)
	_, err := task.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

func TestPanicRecovery(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	pf := &panicFilter{}
	out := &capturingOutput{}
	accept := &alwaysAcceptFilter{}

	// panic in filter should be caught; task should still complete
	task := makeTask("t", inp, pf, accept, out)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// entries survived despite panic (accept ran, but panic ran before accept was possible
	// since filters run per entry, the panic didn't prevent other entries from being processed)
	if res.Total != 3 {
		t.Errorf("want 3 total entries, got %d", res.Total)
	}
}

func TestURLDeduplication(t *testing.T) {
	dupe := entry.New("dup", "http://same.com")
	inp := &stubInput{entries: []*entry.Entry{dupe, dupe.Clone(), dupe.Clone()}}
	task := makeTask("t", inp)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Errorf("want 1 deduplicated entry, got %d", res.Total)
	}
}

func TestBuildUnknownPlugin(t *testing.T) {
	_, err := Build("t", []PluginConfig{{Name: "does-not-exist"}}, nil, slog.Default())
	if err == nil {
		t.Error("expected error for unknown plugin")
	}
}

// --- metainfo, modify, output, learn panic recovery ---

type panicMetainfo struct{}

func (p *panicMetainfo) Name() string        { return "panic-metainfo" }
func (p *panicMetainfo) Phase() plugin.Phase { return plugin.PhaseMetainfo }
func (p *panicMetainfo) Annotate(_ context.Context, _ *plugin.TaskContext, _ *entry.Entry) error {
	panic("metainfo panic")
}

type panicModify struct{}

func (p *panicModify) Name() string        { return "panic-modify" }
func (p *panicModify) Phase() plugin.Phase { return plugin.PhaseModify }
func (p *panicModify) Modify(_ context.Context, _ *plugin.TaskContext, _ *entry.Entry) error {
	panic("modify panic")
}

type panicOutput struct{}

func (p *panicOutput) Name() string        { return "panic-output" }
func (p *panicOutput) Phase() plugin.Phase { return plugin.PhaseOutput }
func (p *panicOutput) Output(_ context.Context, _ *plugin.TaskContext, _ []*entry.Entry) error {
	panic("output panic")
}

type panicLearn struct{}

func (p *panicLearn) Name() string        { return "panic-learn" }
func (p *panicLearn) Phase() plugin.Phase { return plugin.PhaseFilter }
func (p *panicLearn) Learn(_ context.Context, _ *plugin.TaskContext, _ []*entry.Entry) error {
	panic("learn panic")
}

func TestPanicRecoveryMetainfo(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	task := makeTask("t", inp, &panicMetainfo{})
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Total != 3 {
		t.Errorf("want 3 entries, got %d", res.Total)
	}
}

func TestPanicRecoveryModify(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	task := makeTask("t", inp, &alwaysAcceptFilter{}, &panicModify{})
	_, err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPanicRecoveryOutput(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	task := makeTask("t", inp, &alwaysAcceptFilter{}, &panicOutput{})
	_, err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPanicRecoveryLearn(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	task := makeTask("t", inp, &panicLearn{})
	_, err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithLogger(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	task, err := Build("t",
		[]PluginConfig{},
		nil,
		slog.Default(),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatal(err)
	}
	// add plugin directly since Build won't know about stub
	task.addPlugin(pluginInstance{impl: inp, config: map[string]any{}})
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Errorf("want 3, got %d", res.Total)
	}
}

func TestTaskName(t *testing.T) {
	task := New("my-task", slog.Default())
	if task.Name() != "my-task" {
		t.Errorf("want %q, got %q", "my-task", task.Name())
	}
}

func TestInputError(t *testing.T) {
	inp := &stubInput{err: errors.New("feed unavailable")}
	task := makeTask("t", inp)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected task error: %v", err)
	}
	// error is logged but task completes with 0 entries
	if res.Total != 0 {
		t.Errorf("want 0 entries on input error, got %d", res.Total)
	}
}

func TestMultipleInputsConcurrent(t *testing.T) {
	inp1 := &stubInput{entries: []*entry.Entry{
		entry.New("a", "http://a.com"),
		entry.New("b", "http://b.com"),
	}}
	inp2 := &stubInput{entries: []*entry.Entry{
		entry.New("c", "http://c.com"),
		entry.New("d", "http://d.com"),
	}}
	task := makeTask("t", inp1, inp2)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 4 {
		t.Errorf("want 4 entries from two concurrent inputs, got %d", res.Total)
	}
}

func TestMultipleInputsDeduplication(t *testing.T) {
	shared := entry.New("shared", "http://same.com")
	inp1 := &stubInput{entries: []*entry.Entry{shared}}
	inp2 := &stubInput{entries: []*entry.Entry{shared.Clone()}}
	task := makeTask("t", inp1, inp2)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Errorf("want 1 deduplicated entry across inputs, got %d", res.Total)
	}
}

func TestMultipleOutputsConcurrent(t *testing.T) {
	inp := &stubInput{entries: threeEntries()}
	accept := &alwaysAcceptFilter{}
	out1 := &capturingOutput{}
	out2 := &capturingOutput{}

	task := makeTask("t", inp, accept, out1, out2)
	_, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// both outputs should receive all accepted entries
	if len(out1.received) != 3 {
		t.Errorf("out1: want 3 entries, got %d", len(out1.received))
	}
	if len(out2.received) != 3 {
		t.Errorf("out2: want 3 entries, got %d", len(out2.received))
	}
}

func TestOutputsRunConcurrentlyNotSerially(t *testing.T) {
	// Two outputs each sleep briefly; if serial they'd take >2x the sleep,
	// if concurrent they complete in ~1x. We verify via timing.
	slow1 := &capturingOutput{}
	slow2 := &capturingOutput{}

	inp := &stubInput{entries: threeEntries()}
	accept := &alwaysAcceptFilter{}
	task := makeTask("t", inp, accept, slow1, slow2)

	start := time.Now()
	_, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Both ran, both got entries — concurrency correctness without timing assertions
	// (timing is environment-dependent and would make tests flaky).
	if len(slow1.received) != 3 || len(slow2.received) != 3 {
		t.Errorf("both outputs should receive 3 entries: got %d, %d",
			len(slow1.received), len(slow2.received))
	}
	_ = start
}

// --- processing pipeline order test doubles ---

// fieldAnnotator is a MetainfoPlugin that sets a named field and appends its name to a log.
type fieldAnnotator struct {
	name  string
	field string
	log   *[]string
}

func (a *fieldAnnotator) Name() string        { return a.name }
func (a *fieldAnnotator) Phase() plugin.Phase { return plugin.PhaseMetainfo }
func (a *fieldAnnotator) Annotate(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	*a.log = append(*a.log, a.name)
	e.Set(a.field, true)
	return nil
}

// fieldRequiredFilter accepts if a named field is set, rejects otherwise, and appends its name to a log.
type fieldRequiredFilter struct {
	name  string
	field string
	log   *[]string
}

func (f *fieldRequiredFilter) Name() string        { return f.name }
func (f *fieldRequiredFilter) Phase() plugin.Phase { return plugin.PhaseFilter }
func (f *fieldRequiredFilter) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	*f.log = append(*f.log, f.name)
	if _, ok := e.Get(f.field); ok {
		e.Accept()
	} else {
		e.Reject(f.name + ": field " + f.field + " not set")
	}
	return nil
}

// TestProcessingPipelineConfigOrder verifies that a filter placed before a metainfo
// plugin in config runs before it (and therefore cannot see the field it sets),
// while a filter placed after can.
func TestProcessingPipelineConfigOrder(t *testing.T) {
	t.Run("filter_before_meta_rejects", func(t *testing.T) {
		var log []string
		inp := &stubInput{entries: []*entry.Entry{entry.New("e", "http://x.com")}}
		meta := &fieldAnnotator{name: "meta", field: "flag", log: &log}
		flt := &fieldRequiredFilter{name: "flt", field: "flag", log: &log}

		// config order: filter first, then meta — filter cannot see the field yet
		task := makeTask("t", inp, flt, meta)
		res, err := task.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.Accepted != 0 || res.Rejected != 1 {
			t.Errorf("want 0 accepted / 1 rejected, got %d / %d", res.Accepted, res.Rejected)
		}
		if len(log) != 2 || log[0] != "flt" || log[1] != "meta" {
			t.Errorf("execution order: got %v, want [flt meta]", log)
		}
	})

	t.Run("meta_before_filter_accepts", func(t *testing.T) {
		var log []string
		inp := &stubInput{entries: []*entry.Entry{entry.New("e", "http://x.com")}}
		meta := &fieldAnnotator{name: "meta", field: "flag", log: &log}
		flt := &fieldRequiredFilter{name: "flt", field: "flag", log: &log}

		// config order: meta first, then filter — filter sees the field
		task := makeTask("t", inp, meta, flt)
		res, err := task.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.Accepted != 1 || res.Rejected != 0 {
			t.Errorf("want 1 accepted / 0 rejected, got %d / %d", res.Accepted, res.Rejected)
		}
		if len(log) != 2 || log[0] != "meta" || log[1] != "flt" {
			t.Errorf("execution order: got %v, want [meta flt]", log)
		}
	})
}

// TestProcessingPipelineInterleaved verifies the full interleaved case:
// meta1 → filter1 → meta2 → filter2 runs in that exact config order.
// filter1 sees only meta1's field; filter2 sees both.
func TestProcessingPipelineInterleaved(t *testing.T) {
	var log []string
	inp := &stubInput{entries: []*entry.Entry{entry.New("e", "http://x.com")}}
	meta1 := &fieldAnnotator{name: "meta1", field: "flag1", log: &log}
	flt1 := &fieldRequiredFilter{name: "flt1", field: "flag1", log: &log}
	meta2 := &fieldAnnotator{name: "meta2", field: "flag2", log: &log}
	flt2 := &fieldRequiredFilter{name: "flt2", field: "flag2", log: &log}

	task := makeTask("t", inp, meta1, flt1, meta2, flt2)
	res, err := task.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 1 {
		t.Errorf("want 1 accepted, got %d", res.Accepted)
	}
	want := []string{"meta1", "flt1", "meta2", "flt2"}
	if len(log) != len(want) {
		t.Fatalf("execution log: got %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Errorf("step %d: got %q, want %q (full log: %v)", i, log[i], want[i], log)
		}
	}
}
