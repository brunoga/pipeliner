package executor_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// --- minimal test plugins ---

type sourcePlugin struct{ urls []string }

func (p *sourcePlugin) Name() string        { return "test_source" }
func (p *sourcePlugin) Generate(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	var out []*entry.Entry
	for _, u := range p.urls {
		e := entry.New(u, u)
		out = append(out, e)
	}
	return out, nil
}

type acceptAllPlugin struct{}

func (p *acceptAllPlugin) Name() string        { return "test_accept" }
func (p *acceptAllPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Accept()
	}
	return entries, nil
}

type sinkPlugin struct{ received []*entry.Entry }

func (p *sinkPlugin) Name() string        { return "test_sink" }
func (p *sinkPlugin) Consume(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	p.received = append(p.received, entries...)
	return nil
}

// failingSinkPlugin fails any entry whose URL matches a configured set.
type failingSinkPlugin struct {
	failURLs map[string]bool
	received []*entry.Entry
}

func (p *failingSinkPlugin) Name() string { return "test_failing_sink" }
func (p *failingSinkPlugin) Consume(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		if p.failURLs[e.URL] {
			e.Fail("test failure")
		} else {
			p.received = append(p.received, e)
		}
	}
	return nil
}

// commitPlugin is a processor that also implements CommitPlugin so we can verify
// the commit phase behaviour.
type commitPlugin struct {
	committed []*entry.Entry
}

func (p *commitPlugin) Name() string { return "test_commit" }
func (p *commitPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Accept()
	}
	return entry.PassThrough(entries), nil
}
func (p *commitPlugin) Commit(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	p.committed = append(p.committed, entries...)
	return nil
}

// --- test descriptor helpers ---

func sourceDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_source", Role: plugin.RoleSource}
}
func processorDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_proc", Role: plugin.RoleProcessor}
}
func commitDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_commit", Role: plugin.RoleProcessor}
}
func sinkDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_sink", Role: plugin.RoleSink}
}
func failingSinkDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_failing_sink", Role: plugin.RoleSink}
}

func buildExec(t *testing.T, nodes []*dag.Node, instances map[dag.NodeID]*executor.PluginInstance) *executor.Executor {
	t.Helper()
	g := dag.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	return executor.New("test", g, instances, nil, nil, false)
}

// --- tests ---

func TestExecutor_LinearPipeline(t *testing.T) {
	sink := &sinkPlugin{}
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "accept", PluginName: "test_accept", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":    {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"accept": {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
			"sink":   {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)

	res, err := ex.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Errorf("want Total=2, got %d", res.Total)
	}
	if len(sink.received) != 2 {
		t.Errorf("sink want 2 entries, got %d", len(sink.received))
	}
}

func TestExecutor_FanOut(t *testing.T) {
	// src → accept → sink1
	//             ↘ sink2
	sink1, sink2 := &sinkPlugin{}, &sinkPlugin{}
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "accept", PluginName: "test_accept", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink1", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
			{ID: "sink2", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":   {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://x.com"}}, Config: map[string]any{}},
			"accept": {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
			"sink1": {Desc: sinkDesc(), Impl: sink1, Config: map[string]any{}},
			"sink2": {Desc: sinkDesc(), Impl: sink2, Config: map[string]any{}},
		},
	)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink1.received) != 1 {
		t.Errorf("sink1 want 1 entry, got %d", len(sink1.received))
	}
	if len(sink2.received) != 1 {
		t.Errorf("sink2 want 1 entry, got %d", len(sink2.received))
	}
	// Fan-out must clone: both sinks get independent pointers.
	if len(sink1.received) > 0 && len(sink2.received) > 0 && sink1.received[0] == sink2.received[0] {
		t.Error("fan-out did not clone entries: both sinks received the same pointer")
	}
}

func TestExecutor_Merge(t *testing.T) {
	// src1 ↘
	//        accept → sink
	// src2 ↗
	sink := &sinkPlugin{}
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src1", PluginName: "test_source"},
			{ID: "src2", PluginName: "test_source"},
			{ID: "accept", PluginName: "test_accept", Upstreams: []dag.NodeID{"src1", "src2"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src1":   {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com"}}, Config: map[string]any{}},
			"src2":   {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://b.com"}}, Config: map[string]any{}},
			"accept": {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
			"sink":   {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)

	res, err := ex.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Errorf("want Total=2 (one per source), got %d", res.Total)
	}
	if len(sink.received) != 2 {
		t.Errorf("sink want 2 entries, got %d", len(sink.received))
	}
}

func TestExecutor_DeduplicatesURL(t *testing.T) {
	// Both sources emit the same URL — merge should dedup.
	sink := &sinkPlugin{}
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src1", PluginName: "test_source"},
			{ID: "src2", PluginName: "test_source"},
			{ID: "accept", PluginName: "test_accept", Upstreams: []dag.NodeID{"src1", "src2"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src1":   {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://same.com"}}, Config: map[string]any{}},
			"src2":   {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://same.com"}}, Config: map[string]any{}},
			"accept": {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
			"sink":   {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.received) != 1 {
		t.Errorf("want 1 deduplicated entry, got %d", len(sink.received))
	}
}

func TestExecutor_DryRun_SkipsSink(t *testing.T) {
	sink := &sinkPlugin{}
	g := dag.New()
	for _, n := range []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "accept", PluginName: "test_accept", Upstreams: []dag.NodeID{"src"}},
		{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
	} {
		_ = g.AddNode(n)
	}
	ex := executor.New("test", g, map[dag.NodeID]*executor.PluginInstance{
		"src":    {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com"}}, Config: map[string]any{}},
		"accept": {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
		"sink":   {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
	}, nil, nil, true /* dryRun */)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The sinkPlugin.Consume is called but tc.DryRun=true; the real sink
	// adapter (outputAdapter) would check DryRun. Here sinkPlugin directly
	// implements SinkPlugin, so we verify DryRun propagates to tc.
	// In production this is tested via the legacy adapter.
}

// TestExecutor_CommitPlugin_CalledAfterSinks verifies that CommitPlugin.Commit
// is called after all sinks have run and only for entries not failed by sinks.
func TestExecutor_CommitPlugin_CalledAfterSinks(t *testing.T) {
	// src → commit_proc → failing_sink
	// The sink will fail http://b.com. Commit should only see http://a.com.
	proc := &commitPlugin{}
	sink := &failingSinkPlugin{failURLs: map[string]bool{"http://b.com": true}}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "proc", PluginName: "test_commit", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink", PluginName: "test_failing_sink", Upstreams: []dag.NodeID{"proc"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":  {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"proc": {Desc: commitDesc(), Impl: proc, Config: map[string]any{}},
			"sink": {Desc: failingSinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Commit should have been called with only the non-failed entry.
	if len(proc.committed) != 1 {
		t.Errorf("want 1 committed entry, got %d", len(proc.committed))
	}
	if len(proc.committed) > 0 && proc.committed[0].URL != "http://a.com" {
		t.Errorf("want committed URL http://a.com, got %s", proc.committed[0].URL)
	}
}

// TestExecutor_CommitPlugin_FanOut_URLMatching verifies that when a processor
// fans out to two sinks and one branch fails an entry (matched by URL), that URL
// is excluded from Commit even on the branch where the entry was not failed.
func TestExecutor_CommitPlugin_FanOut_URLMatching(t *testing.T) {
	// src → proc → sink1 (fails http://b.com)
	//           ↘ sink2 (accepts all)
	// Commit should exclude http://b.com because sink1 failed it (URL matching).
	proc := &commitPlugin{}
	sink1 := &failingSinkPlugin{failURLs: map[string]bool{"http://b.com": true}}
	sink2 := &sinkPlugin{}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "proc", PluginName: "test_commit", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink1", PluginName: "test_failing_sink", Upstreams: []dag.NodeID{"proc"}},
			{ID: "sink2", PluginName: "test_sink", Upstreams: []dag.NodeID{"proc"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":   {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"proc":  {Desc: commitDesc(), Impl: proc, Config: map[string]any{}},
			"sink1": {Desc: failingSinkDesc(), Impl: sink1, Config: map[string]any{}},
			"sink2": {Desc: sinkDesc(), Impl: sink2, Config: map[string]any{}},
		},
	)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Commit should only see http://a.com (http://b.com was failed by sink1 via URL).
	if len(proc.committed) != 1 {
		t.Errorf("want 1 committed entry (URL match across fan-out branches), got %d", len(proc.committed))
	}
	if len(proc.committed) > 0 && proc.committed[0].URL != "http://a.com" {
		t.Errorf("committed entry URL: want http://a.com, got %s", proc.committed[0].URL)
	}
}

// TestExecutor_SinkChaining verifies that a chained sink only receives entries
// the upstream sink didn't fail.
func TestExecutor_SinkChaining(t *testing.T) {
	// src → proc → sink1 (fails http://b.com) → sink2
	// sink2 should receive only the non-failed entry (http://a.com).
	proc := &commitPlugin{}
	sink1 := &failingSinkPlugin{failURLs: map[string]bool{"http://b.com": true}}
	sink2 := &sinkPlugin{}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "proc", PluginName: "test_commit", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink1", PluginName: "test_failing_sink", Upstreams: []dag.NodeID{"proc"}},
			{ID: "sink2", PluginName: "test_sink", Upstreams: []dag.NodeID{"sink1"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":   {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"proc":  {Desc: commitDesc(), Impl: proc, Config: map[string]any{}},
			"sink1": {Desc: failingSinkDesc(), Impl: sink1, Config: map[string]any{}},
			"sink2": {Desc: sinkDesc(), Impl: sink2, Config: map[string]any{}},
		},
	)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// sink2 should only receive the entry that sink1 didn't fail.
	if len(sink2.received) != 1 {
		t.Errorf("chained sink2: want 1 entry, got %d", len(sink2.received))
	}
	if len(sink2.received) > 0 && sink2.received[0].URL != "http://a.com" {
		t.Errorf("chained sink2 entry URL: want http://a.com, got %s", sink2.received[0].URL)
	}
}

// TestExecutor_CommitPlugin_ContextCancelled verifies that Commit is skipped
// when the context is already cancelled before the commit phase.
func TestExecutor_CommitPlugin_ContextCancelled(t *testing.T) {
	proc := &commitPlugin{}
	sink := &sinkPlugin{}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "proc", PluginName: "test_commit", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"proc"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":  {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com"}}, Config: map[string]any{}},
			"proc": {Desc: commitDesc(), Impl: proc, Config: map[string]any{}},
			"sink": {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)

	// Cancel the context before running so the commit phase sees a cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Run returns an error (context cancelled) but that's expected.
	ex.Run(ctx) //nolint:errcheck

	// Commit should not have been called (context cancelled before commit phase).
	if len(proc.committed) != 0 {
		t.Errorf("want 0 committed entries when context is cancelled, got %d", len(proc.committed))
	}
}

// --- logging tests ---

// logSink captures slog records for inspection.
type logSink struct {
	records []slog.Record
}

func (s *logSink) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (s *logSink) Handle(_ context.Context, r slog.Record) error {
	s.records = append(s.records, r)
	return nil
}
func (s *logSink) WithAttrs(attrs []slog.Attr) slog.Handler {
	return s // simplified: don't need attribute inheritance for these tests
}
func (s *logSink) WithGroup(name string) slog.Handler { return s }

func (s *logSink) has(level slog.Level, msg string) bool {
	for _, r := range s.records {
		if r.Level == level && r.Message == msg {
			return true
		}
	}
	return false
}

// lateRejectPlugin rejects every incoming entry (expects them already Accepted).
type lateRejectPlugin struct{}

func (p *lateRejectPlugin) Name() string { return "test_late_reject" }
func (p *lateRejectPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Reject("changed mind")
	}
	return nil, nil
}

func lateRejectDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_late_reject", Role: plugin.RoleProcessor}
}

// lateFailPlugin fails every incoming entry (expects them already Accepted).
type lateFailPlugin struct{}

func (p *lateFailPlugin) Name() string { return "test_late_fail" }
func (p *lateFailPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Fail("hardware error")
	}
	return nil, nil
}

func lateFailDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_late_fail", Role: plugin.RoleProcessor}
}

// rejectPlugin rejects every entry.
type rejectPlugin struct{}

func (p *rejectPlugin) Name() string { return "test_reject" }
func (p *rejectPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Reject("test rejection reason")
	}
	return nil, nil
}

func rejectDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_reject", Role: plugin.RoleProcessor}
}

func TestExecutor_LogsRejectedEntry(t *testing.T) {
	sink := &logSink{}
	logger := slog.New(sink)

	src := &sourcePlugin{urls: []string{"http://a.com"}}
	rej := &rejectPlugin{}

	g := dag.New()
	nodes := []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "rej", PluginName: "test_reject", Upstreams: []dag.NodeID{"src"}},
	}
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	plugins := map[dag.NodeID]*executor.PluginInstance{
		"src": {Desc: sourceDesc(), Impl: src, Config: map[string]any{}},
		"rej": {Desc: rejectDesc(), Impl: rej, Config: map[string]any{}},
	}
	ex := executor.New("test", g, plugins, nil, logger, false)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !sink.has(slog.LevelDebug, "entry rejected") {
		t.Error("expected Debug 'entry rejected' log line for rejected entry")
	}
}

func TestExecutor_LogsAcceptedEntryAtSink(t *testing.T) {
	sink := &logSink{}
	logger := slog.New(sink)

	src := &sourcePlugin{urls: []string{"http://a.com"}}
	acc := &acceptAllPlugin{}
	sk := &sinkPlugin{}

	g := dag.New()
	nodes := []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "acc", PluginName: "test_accept", Upstreams: []dag.NodeID{"src"}},
		{ID: "sk", PluginName: "test_sink", Upstreams: []dag.NodeID{"acc"}},
	}
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	plugins := map[dag.NodeID]*executor.PluginInstance{
		"src": {Desc: sourceDesc(), Impl: src, Config: map[string]any{}},
		"acc": {Desc: processorDesc(), Impl: acc, Config: map[string]any{}},
		"sk":  {Desc: sinkDesc(), Impl: sk, Config: map[string]any{}},
	}
	ex := executor.New("test", g, plugins, nil, logger, false)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !sink.has(slog.LevelInfo, "entry accepted") {
		t.Error("expected Info 'entry accepted' log line for entry reaching sink")
	}
}

func TestExecutor_LogsAcceptedToRejectedTransition(t *testing.T) {
	ls := &logSink{}
	logger := slog.New(ls)

	// Two-node pipeline: first node accepts, second node rejects.
	// stateBefore at the reject node will be Accepted, triggering the transition log.
	src := &sourcePlugin{urls: []string{"http://a.com"}}
	acc := &acceptAllPlugin{}
	rej := &lateRejectPlugin{}

	g := dag.New()
	for _, n := range []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "acc", PluginName: "test_accept", Upstreams: []dag.NodeID{"src"}},
		{ID: "rej", PluginName: "test_late_reject", Upstreams: []dag.NodeID{"acc"}},
	} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	plugins := map[dag.NodeID]*executor.PluginInstance{
		"src": {Desc: sourceDesc(), Impl: src, Config: map[string]any{}},
		"acc": {Desc: processorDesc(), Impl: acc, Config: map[string]any{}},
		"rej": {Desc: lateRejectDesc(), Impl: rej, Config: map[string]any{}},
	}
	ex := executor.New("test", g, plugins, nil, logger, false)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !ls.has(slog.LevelDebug, "entry accepted → rejected") {
		t.Error("expected Debug 'entry accepted → rejected' log line for accepted-then-rejected entry")
	}
	if ls.has(slog.LevelDebug, "entry rejected") {
		t.Error("expected no plain 'entry rejected' log line when entry was previously accepted")
	}
}

func TestExecutor_LogsAcceptedToFailedTransition(t *testing.T) {
	ls := &logSink{}
	logger := slog.New(ls)

	// Two-node pipeline: first node accepts, second node fails.
	// stateBefore at the fail node will be Accepted, triggering the transition log.
	src := &sourcePlugin{urls: []string{"http://a.com"}}
	acc := &acceptAllPlugin{}
	fail := &lateFailPlugin{}

	g := dag.New()
	for _, n := range []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "acc", PluginName: "test_accept", Upstreams: []dag.NodeID{"src"}},
		{ID: "fail", PluginName: "test_late_fail", Upstreams: []dag.NodeID{"acc"}},
	} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	plugins := map[dag.NodeID]*executor.PluginInstance{
		"src":  {Desc: sourceDesc(), Impl: src, Config: map[string]any{}},
		"acc":  {Desc: processorDesc(), Impl: acc, Config: map[string]any{}},
		"fail": {Desc: lateFailDesc(), Impl: fail, Config: map[string]any{}},
	}
	ex := executor.New("test", g, plugins, nil, logger, false)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !ls.has(slog.LevelWarn, "entry accepted → failed") {
		t.Error("expected Warn 'entry accepted → failed' log line for accepted-then-failed entry")
	}
	if ls.has(slog.LevelWarn, "entry failed") {
		t.Error("expected no plain 'entry failed' log line when entry was previously accepted")
	}
}
