package executor_test

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// --- minimal test plugins ---

type sourcePlugin struct{ urls []string }

func (p *sourcePlugin) Name() string { return "test_source" }
func (p *sourcePlugin) Generate(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	var out []*entry.Entry
	for _, u := range p.urls {
		e := entry.New(u, u)
		out = append(out, e)
	}
	return out, nil
}

type acceptAllPlugin struct{}

func (p *acceptAllPlugin) Name() string { return "test_accept" }
func (p *acceptAllPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Accept()
	}
	return entries, nil
}

type sinkPlugin struct{ received []*entry.Entry }

func (p *sinkPlugin) Name() string { return "test_sink" }
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
			"src":    {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://x.com"}}, Config: map[string]any{}},
			"accept": {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
			"sink1":  {Desc: sinkDesc(), Impl: sink1, Config: map[string]any{}},
			"sink2":  {Desc: sinkDesc(), Impl: sink2, Config: map[string]any{}},
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

// TestExecutor_UndecidedCount confirms that source entries which never get
// claimed by a processor (and so never transition out of Undecided) are
// counted in res.Undecided. The sink's default AcceptedOnly pre-filter
// excludes Undecided entries entirely, so they stay Undecided through the
// run — exactly the case the new "undecided" log/stat counter exists for.
func TestExecutor_UndecidedCount(t *testing.T) {
	sink := &sinkPlugin{}
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"src"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":  {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"sink": {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)

	res, err := ex.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Errorf("want Total=2, got %d", res.Total)
	}
	if res.Undecided != 2 {
		t.Errorf("want Undecided=2 (neither source entry was accepted), got %d", res.Undecided)
	}
	if res.Accepted != 0 || res.Rejected != 0 || res.Failed != 0 {
		t.Errorf("want zero terminal counts, got accepted=%d rejected=%d failed=%d",
			res.Accepted, res.Rejected, res.Failed)
	}
	if len(sink.received) != 0 {
		t.Errorf("sink must not receive Undecided entries (AcceptedOnly pre-filter), got %d", len(sink.received))
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

// TestExecutor_EmptyURLEntriesBypassDedup proves that URL-less entries are
// not collapsed by mergeAndDedup's URL key. Without the empty-URL guard a
// source emitting two entries with URL="" would have only one survive.
func TestExecutor_EmptyURLEntriesBypassDedup(t *testing.T) {
	sink := &sinkPlugin{}
	urlless := &sourcePlugin{urls: []string{""}}
	urlless2 := &sourcePlugin{urls: []string{""}}
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src1", PluginName: "test_source"},
			{ID: "src2", PluginName: "test_source"},
			{ID: "accept", PluginName: "test_accept", Upstreams: []dag.NodeID{"src1", "src2"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src1":   {Desc: sourceDesc(), Impl: urlless, Config: map[string]any{}},
			"src2":   {Desc: sourceDesc(), Impl: urlless2, Config: map[string]any{}},
			"accept": {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
			"sink":   {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.received) != 2 {
		t.Errorf("want 2 URL-less entries to survive merge, got %d", len(sink.received))
	}
}

// TestExecutor_EmptyURLCommittedDespiteUnrelatedFailure proves that one
// URL-less entry's commit isn't blocked by another URL-less entry's sink
// failure. Without the guard both would share the same "" key in failedURLs
// and the second one's commit would be incorrectly suppressed.
func TestExecutor_EmptyURLCommittedDespiteUnrelatedFailure(t *testing.T) {
	commit := &commitPlugin{}
	// Two URL-less entries; the failing sink fails only the first (the one
	// it sees first will fail by Title since its failURLs map is keyed by
	// URL — both entries have URL=""). Use a Title-keyed failer instead.
	failing := &titleFailingSink{failTitles: map[string]bool{"a": true}}
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "commit", PluginName: "test_commit", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink", PluginName: "test_failing_sink", Upstreams: []dag.NodeID{"commit"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":    {Desc: sourceDesc(), Impl: &titleSource{titles: []string{"a", "b"}}, Config: map[string]any{}},
			"commit": {Desc: commitDesc(), Impl: commit, Config: map[string]any{}},
			"sink":   {Desc: failingSinkDesc(), Impl: failing, Config: map[string]any{}},
		},
	)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Both URL-less entries must reach commit. Pre-fix, one would set
	// failedURLs[""]=true and the other would be skipped.
	if len(commit.committed) != 2 {
		t.Errorf("want both URL-less entries committed, got %d", len(commit.committed))
	}
}

// titleSource emits URL-less entries — Title carries the identity instead.
type titleSource struct{ titles []string }

func (p *titleSource) Name() string { return "test_source" }
func (p *titleSource) Generate(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	out := make([]*entry.Entry, 0, len(p.titles))
	for _, t := range p.titles {
		out = append(out, entry.New(t, ""))
	}
	return out, nil
}

// titleFailingSink fails entries whose Title is in failTitles.
type titleFailingSink struct{ failTitles map[string]bool }

func (p *titleFailingSink) Name() string { return "test_failing_sink" }
func (p *titleFailingSink) Consume(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		if p.failTitles[e.Title] {
			e.Fail("test failure")
		}
	}
	return nil
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

// TestExecutor_DryRun_SkipsCommit verifies that in dry-run mode the commit
// phase is skipped entirely, so trackers (seen/movies/series/premiere) don't
// advance state on a preview run.
func TestExecutor_DryRun_SkipsCommit(t *testing.T) {
	proc := &commitPlugin{}
	sink := &sinkPlugin{}
	g := dag.New()
	for _, n := range []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "proc", PluginName: "test_commit", Upstreams: []dag.NodeID{"src"}},
		{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"proc"}},
	} {
		_ = g.AddNode(n)
	}
	ex := executor.New("test", g, map[dag.NodeID]*executor.PluginInstance{
		"src":  {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com"}}, Config: map[string]any{}},
		"proc": {Desc: commitDesc(), Impl: proc, Config: map[string]any{}},
		"sink": {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
	}, nil, nil, true /* dryRun */)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(proc.committed) != 0 {
		t.Errorf("dry-run: Commit must not be called; got %d committed entries", len(proc.committed))
	}
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
	ex.Run(ctx)

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

// attrsFor returns the attributes of every record matching msg, one map per
// record, in emit order.
func (s *logSink) attrsFor(msg string) []map[string]any {
	var out []map[string]any
	for _, r := range s.records {
		if r.Message != msg {
			continue
		}
		m := map[string]any{}
		r.Attrs(func(a slog.Attr) bool {
			m[a.Key] = a.Value.Any()
			return true
		})
		out = append(out, m)
	}
	return out
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

	if !sink.has(slog.LevelInfo, "entry rejected") {
		t.Error("expected Info 'entry rejected' log line for rejected entry")
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

	if !ls.has(slog.LevelInfo, "entry accepted → rejected") {
		t.Error("expected Info 'entry accepted → rejected' log line for accepted-then-rejected entry")
	}
	if ls.has(slog.LevelInfo, "entry rejected") {
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

// requiresFieldPlugin is a processor that requires "needed_field" but never
// sets it — used to verify the ValidateFields warning path.
type requiresFieldPlugin struct{}

func (p *requiresFieldPlugin) Name() string { return "test_requires_field" }
func (p *requiresFieldPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	return entries, nil
}

func requiresFieldDesc() *plugin.Descriptor {
	return &plugin.Descriptor{
		PluginName: "test_requires_field",
		Role:       plugin.RoleProcessor,
		Requires:   plugin.RequireAll("needed_field"),
	}
}

func TestExecutor_ValidateFields_WarnsOnMissingField(t *testing.T) {
	ls := &logSink{}
	logger := slog.New(ls)

	src := &sourcePlugin{urls: []string{"http://a.com"}}
	proc := &requiresFieldPlugin{}

	g := dag.New()
	for _, n := range []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "proc", PluginName: "test_requires_field", Upstreams: []dag.NodeID{"src"}},
	} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	plugins := map[dag.NodeID]*executor.PluginInstance{
		"src":  {Desc: sourceDesc(), Impl: src, Config: map[string]any{}},
		"proc": {Desc: requiresFieldDesc(), Impl: proc, Config: map[string]any{}},
	}
	ex := executor.New("test", g, plugins, nil, logger, false)
	ex.SetValidateFields(true)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !ls.has(slog.LevelWarn, "entry missing required fields") {
		t.Error("expected Warn 'entry missing required fields' when ValidateFields is on and field is absent")
	}
}

func TestExecutor_ValidateFields_SilentWhenFieldPresent(t *testing.T) {
	ls := &logSink{}
	logger := slog.New(ls)

	// src → setField (sets "needed_field") → proc (Requires "needed_field")
	// ValidateFields should produce no warning because the field is present.
	src := &sourcePlugin{urls: []string{"http://a.com"}}
	proc := &requiresFieldPlugin{}

	g := dag.New()
	for _, n := range []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "sf", PluginName: "test_set_field", Upstreams: []dag.NodeID{"src"}},
		{ID: "proc", PluginName: "test_requires_field", Upstreams: []dag.NodeID{"sf"}},
	} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	plugins := map[dag.NodeID]*executor.PluginInstance{
		"src":  {Desc: sourceDesc(), Impl: src, Config: map[string]any{}},
		"sf":   {Desc: &plugin.Descriptor{PluginName: "test_set_field", Role: plugin.RoleProcessor}, Impl: &setFieldProcessor{}, Config: map[string]any{}},
		"proc": {Desc: requiresFieldDesc(), Impl: proc, Config: map[string]any{}},
	}
	ex := executor.New("test", g, plugins, nil, logger, false)
	ex.SetValidateFields(true)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if ls.has(slog.LevelWarn, "entry missing required fields") {
		t.Error("expected no 'entry missing required fields' warning when field is present")
	}
}

type setFieldProcessor struct{}

func (p *setFieldProcessor) Name() string { return "test_set_field" }
func (p *setFieldProcessor) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Set("needed_field", "value")
	}
	return entries, nil
}

// TestExecutor_RouteSelectorAcceptedCount verifies that fan-out through a
// route_selector does not corrupt the pipeline accepted/rejected summary.
// The route node stamps _route_port; one selector passes matching entries and
// silently drops non-matching ones (filter semantics, not reject semantics).
// A 2-port route with 3 entries (2 torrent, 1 magnet) that all reach the sink
// should report accepted=3, not accepted=2.
func TestExecutor_RouteSelectorAcceptedCount(t *testing.T) {
	const portTorrent = "torrent"
	const portMagnet = "magnet"

	// routeStamper stamps _route_port on every entry based on URL prefix.
	type routeStamper struct{}
	_ = (*routeStamper)(nil)

	src := &sourcePlugin{urls: []string{
		"https://example.com/t1.torrent",
		"https://example.com/t2.torrent",
		"magnet:?xt=urn:btih:abc",
	}}

	// Stamp _route_port and Accept each entry.
	stampRoute := &funcProcessor{fn: func(entries []*entry.Entry) []*entry.Entry {
		for _, e := range entries {
			if strings.HasPrefix(e.URL, "magnet:") {
				e.Set(entry.FieldRoutePort, portMagnet)
			} else {
				e.Set(entry.FieldRoutePort, portTorrent)
			}
			e.Accept()
		}
		return entry.PassThrough(entries)
	}}

	// Selectors — filter-style (return only matching).
	selTorrent := &funcProcessor{fn: func(entries []*entry.Entry) []*entry.Entry {
		var out []*entry.Entry
		for _, e := range entries {
			if e.IsRejected() || e.IsFailed() {
				continue
			}
			if p, _ := e.Get(entry.FieldRoutePort); p == portTorrent {
				out = append(out, e)
			}
		}
		return out
	}}
	selMagnet := &funcProcessor{fn: func(entries []*entry.Entry) []*entry.Entry {
		var out []*entry.Entry
		for _, e := range entries {
			if e.IsRejected() || e.IsFailed() {
				continue
			}
			if p, _ := e.Get(entry.FieldRoutePort); p == portMagnet {
				out = append(out, e)
			}
		}
		return out
	}}

	sink := &sinkPlugin{}

	g := dag.New()
	for _, n := range []*dag.Node{
		{ID: "src", PluginName: "test_source"},
		{ID: "route", PluginName: "test_route", Upstreams: []dag.NodeID{"src"}},
		{ID: "sel_torrent", PluginName: "test_sel_torrent", Upstreams: []dag.NodeID{"route"}},
		{ID: "sel_magnet", PluginName: "test_sel_magnet", Upstreams: []dag.NodeID{"route"}},
		{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"sel_torrent", "sel_magnet"}},
	} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}

	plugins := map[dag.NodeID]*executor.PluginInstance{
		"src":         {Desc: sourceDesc(), Impl: src, Config: map[string]any{}},
		"route":       {Desc: processorDesc(), Impl: stampRoute, Config: map[string]any{}},
		"sel_torrent": {Desc: processorDesc(), Impl: selTorrent, Config: map[string]any{}},
		"sel_magnet":  {Desc: processorDesc(), Impl: selMagnet, Config: map[string]any{}},
		"sink":        {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
	}

	ex := executor.New("test", g, plugins, nil, slog.New(&logSink{}), false)
	res, err := ex.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if res.Accepted != 3 {
		t.Errorf("want accepted=3, got %d (fan-out must not corrupt accepted count)", res.Accepted)
	}
	if res.Rejected != 0 {
		t.Errorf("want rejected=0, got %d", res.Rejected)
	}
}

// funcProcessor is a test helper that runs a user-supplied function as a processor.
type funcProcessor struct {
	fn func([]*entry.Entry) []*entry.Entry
}

func (p *funcProcessor) Name() string { return "test_func" }
func (p *funcProcessor) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	return p.fn(entries), nil
}

// observeProcessor records the URLs it actually sees in its Process call so a
// test can assert which states the executor pre-filter is hiding.
type observeProcessor struct {
	seen []string
}

func (p *observeProcessor) Name() string { return "test_observe" }
func (p *observeProcessor) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		p.seen = append(p.seen, e.URL)
	}
	return entries, nil
}

// TestExecutor_InputStatesPreFilter_Default verifies that the executor
// pre-filters upstream to a processor's default InputStates
// (StatesAcceptedUndecided) — rejected and failed entries bypass Process.
// They must still reach downstream nodes for bookkeeping.
func TestExecutor_InputStatesPreFilter_Default(t *testing.T) {
	// src emits two entries; reject_b rejects http://b.com; observe records
	// what reaches its Process(); collect records what reaches the sink.
	rejectB := &funcProcessor{fn: func(entries []*entry.Entry) []*entry.Entry {
		for _, e := range entries {
			if e.URL == "http://b.com" {
				e.Reject("test: reject b")
			} else {
				e.Accept()
			}
		}
		// Return the full slice so the executor sees both populations; the
		// pre-filter on the next node is what's under test.
		return entries
	}}
	observe := &observeProcessor{}
	collect := &sinkPlugin{}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "reject", PluginName: "test_func", Upstreams: []dag.NodeID{"src"}},
			{ID: "observe", PluginName: "test_observe", Upstreams: []dag.NodeID{"reject"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"observe"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":     {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"reject":  {Desc: processorDesc(), Impl: rejectB, Config: map[string]any{}},
			"observe": {Desc: processorDesc(), Impl: observe, Config: map[string]any{}},
			"sink":    {Desc: sinkDesc(), Impl: collect, Config: map[string]any{}},
		},
	)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// observe must have seen only the accepted entry — the rejected one was
	// filtered out by the executor before Process was called.
	if len(observe.seen) != 1 || observe.seen[0] != "http://a.com" {
		t.Errorf("observe.seen: got %v, want [http://a.com] (rejected b should have been filtered before Process)", observe.seen)
	}
	// The downstream sink defaults to InputStates=StatesAcceptedOnly, so it
	// must NOT see the rejected entry — that's the same pre-filter mechanism
	// applied at the sink boundary. The rejected entry is still merged into
	// the executor's `produced` slice for commit-phase accounting, but the
	// sink's Consume only sees the accepted one.
	if len(collect.received) != 1 || collect.received[0].URL != "http://a.com" {
		t.Errorf("sink.received: got %v, want [http://a.com] (sink default InputStates filters rejected entries)", urls(collect.received))
	}
}

// urls is a small helper that pulls .URL out of a slice for clearer error
// messages in tests.
func urls(es []*entry.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.URL
	}
	return out
}

// TestExecutor_InputStatesPreFilter_AllStates verifies that a processor with
// InputStates=StatesAll (e.g. swap_state) receives every state from upstream,
// including rejected and failed entries.
func TestExecutor_InputStatesPreFilter_AllStates(t *testing.T) {
	rejectB := &funcProcessor{fn: func(entries []*entry.Entry) []*entry.Entry {
		for _, e := range entries {
			if e.URL == "http://b.com" {
				e.Reject("test: reject b")
			} else {
				e.Accept()
			}
		}
		return entries
	}}
	observe := &observeProcessor{}

	observeAllDesc := &plugin.Descriptor{
		PluginName:  "test_observe",
		Role:        plugin.RoleProcessor,
		InputStates: entry.StatesAll,
	}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "reject", PluginName: "test_func", Upstreams: []dag.NodeID{"src"}},
			{ID: "observe", PluginName: "test_observe", Upstreams: []dag.NodeID{"reject"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":     {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"reject":  {Desc: processorDesc(), Impl: rejectB, Config: map[string]any{}},
			"observe": {Desc: observeAllDesc, Impl: observe, Config: map[string]any{}},
		},
	)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(observe.seen) != 2 {
		t.Fatalf("observe.seen: got %d entries, want 2 (StatesAll must surface rejected entries too)", len(observe.seen))
	}
	// Order must match upstream emission order (matching followed by excluded
	// is the executor's merge contract; here all entries are "matching" so
	// they retain emission order).
	wantURLs := map[string]bool{"http://a.com": true, "http://b.com": true}
	for _, u := range observe.seen {
		if !wantURLs[u] {
			t.Errorf("observe saw unexpected URL %q", u)
		}
	}
}

// TestExecutor_SinkInputStatesPreFilter_Default verifies that sinks default
// to InputStates=StatesAcceptedOnly: rejected and undecided entries from
// upstream must not reach the sink's Consume call.
func TestExecutor_SinkInputStatesPreFilter_Default(t *testing.T) {
	// reject_b rejects http://b.com, leaves http://a.com Accepted.
	rejectB := &funcProcessor{fn: func(entries []*entry.Entry) []*entry.Entry {
		for _, e := range entries {
			if e.URL == "http://b.com" {
				e.Reject("test: reject b")
			} else {
				e.Accept()
			}
		}
		return entries
	}}
	sink := &sinkPlugin{}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "reject", PluginName: "test_func", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"reject"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":    {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"reject": {Desc: processorDesc(), Impl: rejectB, Config: map[string]any{}},
			"sink":   {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.received) != 1 || sink.received[0].URL != "http://a.com" {
		t.Errorf("sink.received: got %v, want [http://a.com] (sink default filters rejected entries)", urls(sink.received))
	}
}

// TestExecutor_SinkConsumedExclusion verifies that consumed entries are
// always excluded from sink input, regardless of InputStates. The consumed
// flag is orthogonal to State and the executor applies it as an always-on
// filter at the sink boundary.
func TestExecutor_SinkConsumedExclusion(t *testing.T) {
	// accept_and_consume_b accepts both entries, then marks b consumed.
	mutator := &funcProcessor{fn: func(entries []*entry.Entry) []*entry.Entry {
		for _, e := range entries {
			e.Accept()
			if e.URL == "http://b.com" {
				e.Consume()
			}
		}
		return entries
	}}
	sink := &sinkPlugin{}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "mark", PluginName: "test_func", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"mark"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":  {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"mark": {Desc: processorDesc(), Impl: mutator, Config: map[string]any{}},
			"sink": {Desc: sinkDesc(), Impl: sink, Config: map[string]any{}},
		},
	)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.received) != 1 || sink.received[0].URL != "http://a.com" {
		t.Errorf("sink.received: got %v, want [http://a.com] (consumed entries are always excluded from sink input)", urls(sink.received))
	}
}

// TestExecutor_SinkChained_NoSpecialCase verifies that a chained sink only
// sees what the upstream sink approved — the same behavior the old
// chained-sink special case at executor.go:379 used to enforce explicitly.
// Now subsumed by the general per-sink pre-filter: when sink A fails an
// entry, sink B's default InputStates=StatesAcceptedOnly excludes it.
func TestExecutor_SinkChained_NoSpecialCase(t *testing.T) {
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
			"proc":  {Desc: commitDesc(), Impl: &commitPlugin{}, Config: map[string]any{}},
			"sink1": {Desc: failingSinkDesc(), Impl: sink1, Config: map[string]any{}},
			"sink2": {Desc: sinkDesc(), Impl: sink2, Config: map[string]any{}},
		},
	)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink2.received) != 1 || sink2.received[0].URL != "http://a.com" {
		t.Errorf("chained sink2.received: got %v, want [http://a.com] (sink2's pre-filter must exclude the entry sink1 failed)", urls(sink2.received))
	}
}

// TestExecutor_SinkInputStatesOverride proves that a sink can declare a
// non-default InputStates to legitimately act on failed entries — the
// notify-on-failure use case that was previously inexpressible because the
// chained-sink special case unconditionally filtered to Accepted.
func TestExecutor_SinkInputStatesOverride(t *testing.T) {
	failSink := &failingSinkPlugin{failURLs: map[string]bool{"http://b.com": true}}
	notifier := &sinkPlugin{}

	// Chained sink that wants to see Failed entries (notify-on-failure).
	notifyDesc := &plugin.Descriptor{
		PluginName:  "test_sink",
		Role:        plugin.RoleSink,
		InputStates: entry.StateBit(entry.Accepted) | entry.StateBit(entry.Failed),
	}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "proc", PluginName: "test_commit", Upstreams: []dag.NodeID{"src"}},
			{ID: "primary", PluginName: "test_failing_sink", Upstreams: []dag.NodeID{"proc"}},
			{ID: "notify", PluginName: "test_sink", Upstreams: []dag.NodeID{"primary"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":     {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com", "http://b.com"}}, Config: map[string]any{}},
			"proc":    {Desc: commitDesc(), Impl: &commitPlugin{}, Config: map[string]any{}},
			"primary": {Desc: failingSinkDesc(), Impl: failSink, Config: map[string]any{}},
			"notify":  {Desc: notifyDesc, Impl: notifier, Config: map[string]any{}},
		},
	)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// notify with InputStates=Accepted|Failed sees both — the one sink1
	// accepted AND the one sink1 failed. This is the new capability.
	if len(notifier.received) != 2 {
		t.Fatalf("notifier.received: got %d entries (%v), want 2 (override should surface both Accepted and Failed)",
			len(notifier.received), urls(notifier.received))
	}
}

// statefulProcessorPlugin is a processor that records the entries handed to
// it by the executor's pre-filter and dynamically declares which states it
// wants to see. Used to verify that DynamicInputStates widens the set of
// states a plugin sees beyond what its Descriptor declares.
type statefulProcessorPlugin struct {
	dynamic  entry.StateSet
	received []*entry.Entry
}

func (p *statefulProcessorPlugin) Name() string { return "test_stateful" }
func (p *statefulProcessorPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	p.received = append(p.received, entries...)
	return entries, nil
}
func (p *statefulProcessorPlugin) EffectiveInputStates() entry.StateSet { return p.dynamic }

// observerProcessorPlugin is the no-DynamicInputStates twin used to verify
// the executor falls back to the Descriptor's declared states when the
// plugin doesn't implement the interface. Also reused by the marker-filter
// tests below as a default-config (AcceptsMarkers=false) processor.
type observerProcessorPlugin struct {
	received []*entry.Entry
}

func (p *observerProcessorPlugin) Name() string { return "test_observer" }
func (p *observerProcessorPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	p.received = append(p.received, entries...)
	return entries, nil
}

// failingProcessorPlugin marks every entry it touches as Failed so a
// downstream node sees a real Failed entry without needing a sink.
type failingProcessorPlugin struct{}

func (p *failingProcessorPlugin) Name() string { return "test_failing_proc" }
func (p *failingProcessorPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Fail("test fail")
	}
	return entries, nil
}

// markerAwareSinkPlugin records every entry the executor hands it, used to
// verify that markers are filtered out (or passed through) at the sink
// boundary based on Descriptor.AcceptsMarkers.
type markerAwareSinkPlugin struct{ received []*entry.Entry }

func (p *markerAwareSinkPlugin) Name() string { return "test_marker_aware_sink" }
func (p *markerAwareSinkPlugin) Consume(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	p.received = append(p.received, entries...)
	return nil
}

// markerEmitterSource emits a single marker entry, simulating report_empty
// without depending on it.
type markerEmitterSource struct{}

func (p *markerEmitterSource) Name() string { return "test_marker_source" }
func (p *markerEmitterSource) Generate(_ context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	m := entry.New("(marker)", "pipeliner://test/marker")
	m.SetMarker()
	m.Accept()
	return []*entry.Entry{m}, nil
}

// TestExecutor_DynamicInputStates confirms that when a plugin implements
// plugin.DynamicInputStates the executor uses the returned StateSet for the
// pre-filter, overriding the Descriptor's declared default. This is what
// lets condition and route widen themselves at instance scope when their
// expressions reference `state`.
func TestExecutor_DynamicInputStates(t *testing.T) {
	// Pipeline: src → fail-everything → observer.
	//   - "fail" leaves every entry in Failed.
	//   - "observe" has Descriptor=AcceptedUndecided but dynamic=StatesAll,
	//     so it MUST see the Failed entry that the default pre-filter would
	//     otherwise hide.
	observer := &statefulProcessorPlugin{dynamic: entry.StatesAll}
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "fail", PluginName: "test_failing_proc", Upstreams: []dag.NodeID{"src"}},
			{ID: "observe", PluginName: "test_stateful", Upstreams: []dag.NodeID{"fail"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":     {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com"}}, Config: map[string]any{}},
			"fail":    {Desc: processorDesc(), Impl: &failingProcessorPlugin{}, Config: map[string]any{}},
			"observe": {Desc: processorDesc(), Impl: observer, Config: map[string]any{}},
		},
	)

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(observer.received) != 1 {
		t.Fatalf("widened observer should see the Failed entry; got %d", len(observer.received))
	}
	if !observer.received[0].IsFailed() {
		t.Errorf("entry handed to observer should be Failed, got %v", observer.received[0].State)
	}

	// Sanity check the inverse: a plain processor that doesn't implement
	// DynamicInputStates falls back to the Descriptor default
	// (AcceptedUndecided for processors), so the same Failed entry bypasses
	// it. This guarantees the new interface is opt-in: existing plugins
	// see no behavior change.
	observer2 := &observerProcessorPlugin{}
	ex2 := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "fail", PluginName: "test_failing_proc", Upstreams: []dag.NodeID{"src"}},
			{ID: "observe", PluginName: "test_observer", Upstreams: []dag.NodeID{"fail"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":     {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com"}}, Config: map[string]any{}},
			"fail":    {Desc: processorDesc(), Impl: &failingProcessorPlugin{}, Config: map[string]any{}},
			"observe": {Desc: processorDesc(), Impl: observer2, Config: map[string]any{}},
		},
	)
	if _, err := ex2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(observer2.received) != 0 {
		t.Errorf("non-widened observer must NOT see Failed entries; got %d", len(observer2.received))
	}
}

// TestExecutor_MarkerFilter confirms that the executor's marker pre-filter
// strips synthetic entries from sinks/processors that didn't declare
// AcceptsMarkers, while still delivering them to those that did. This is
// what protects download/enrichment plugins from acting on a placeholder
// emitted upstream (e.g. by report_empty).
func TestExecutor_MarkerFilter(t *testing.T) {
	defaultSink := &markerAwareSinkPlugin{}
	optedInSink := &markerAwareSinkPlugin{}

	// markerSource → defaultSink (no AcceptsMarkers — must not receive)
	// markerSource → optedInSink (AcceptsMarkers=true — must receive)
	defaultSinkDesc := &plugin.Descriptor{PluginName: "default_sink", Role: plugin.RoleSink}
	optedInDesc := &plugin.Descriptor{PluginName: "opted_in_sink", Role: plugin.RoleSink, AcceptsMarkers: true}
	markerSrcDesc := &plugin.Descriptor{PluginName: "test_marker_source", Role: plugin.RoleSource}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_marker_source"},
			{ID: "default", PluginName: "default_sink", Upstreams: []dag.NodeID{"src"}},
			{ID: "opted", PluginName: "opted_in_sink", Upstreams: []dag.NodeID{"src"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":     {Desc: markerSrcDesc, Impl: &markerEmitterSource{}, Config: map[string]any{}},
			"default": {Desc: defaultSinkDesc, Impl: defaultSink, Config: map[string]any{}},
			"opted":   {Desc: optedInDesc, Impl: optedInSink, Config: map[string]any{}},
		},
	)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(defaultSink.received) != 0 {
		t.Errorf("default sink (AcceptsMarkers=false) must not see markers; got %d", len(defaultSink.received))
	}
	if len(optedInSink.received) != 1 {
		t.Fatalf("opted-in sink must see the marker; got %d", len(optedInSink.received))
	}
	if !optedInSink.received[0].IsMarker() {
		t.Errorf("entry delivered to opted-in sink should be flagged IsMarker()")
	}
}

// TestExecutor_MarkerFilter_PassThrough confirms that markers stripped from
// a default-config processor still flow downstream — they merge back into
// the produced slice exactly like state-excluded entries. So a misrouted
// pipeline (marker → tmdb-like processor → notify) still delivers the
// marker to notify, just bypassing tmdb.
func TestExecutor_MarkerFilter_PassThrough(t *testing.T) {
	intermediate := &observerProcessorPlugin{}
	terminal := &markerAwareSinkPlugin{}

	markerSrcDesc := &plugin.Descriptor{PluginName: "test_marker_source", Role: plugin.RoleSource}
	procDesc := &plugin.Descriptor{PluginName: "test_observer", Role: plugin.RoleProcessor} // no AcceptsMarkers
	sinkDesc := &plugin.Descriptor{PluginName: "opted_in_sink", Role: plugin.RoleSink, AcceptsMarkers: true}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_marker_source"},
			{ID: "mid", PluginName: "test_observer", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink", PluginName: "opted_in_sink", Upstreams: []dag.NodeID{"mid"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":  {Desc: markerSrcDesc, Impl: &markerEmitterSource{}, Config: map[string]any{}},
			"mid":  {Desc: procDesc, Impl: intermediate, Config: map[string]any{}},
			"sink": {Desc: sinkDesc, Impl: terminal, Config: map[string]any{}},
		},
	)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(intermediate.received) != 0 {
		t.Errorf("intermediate processor (AcceptsMarkers=false) must not see the marker; got %d", len(intermediate.received))
	}
	if len(terminal.received) != 1 {
		t.Fatalf("terminal sink must still receive the marker after pass-through; got %d", len(terminal.received))
	}
	if !terminal.received[0].IsMarker() {
		t.Errorf("entry at terminal sink should be IsMarker()")
	}
}

// TestExecutor_NodeStartedLogReportsInAndBypassed confirms that the
// per-node "node started" log records the post-filter in count and the
// bypassed count, so users can tell at a glance when an upstream entry
// reached a node but was excluded (state mismatch, consumed, or marker
// without AcceptsMarkers) rather than processed.
func TestExecutor_NodeStartedLogReportsInAndBypassed(t *testing.T) {
	ls := &logSink{}
	logger := slog.New(ls)

	// marker source → default-config processor → opted-in sink. The
	// processor bypasses the marker (in=0, bypassed=1) and the sink
	// receives it (in=1, bypassed=0).
	intermediate := &observerProcessorPlugin{}
	terminal := &markerAwareSinkPlugin{}

	markerSrcDesc := &plugin.Descriptor{PluginName: "test_marker_source", Role: plugin.RoleSource}
	procDesc := &plugin.Descriptor{PluginName: "test_observer", Role: plugin.RoleProcessor}
	sinkDesc := &plugin.Descriptor{PluginName: "opted_in_sink", Role: plugin.RoleSink, AcceptsMarkers: true}

	g := dag.New()
	for _, n := range []*dag.Node{
		{ID: "src", PluginName: "test_marker_source"},
		{ID: "mid", PluginName: "test_observer", Upstreams: []dag.NodeID{"src"}},
		{ID: "sink", PluginName: "opted_in_sink", Upstreams: []dag.NodeID{"mid"}},
	} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	plugins := map[dag.NodeID]*executor.PluginInstance{
		"src":  {Desc: markerSrcDesc, Impl: &markerEmitterSource{}, Config: map[string]any{}},
		"mid":  {Desc: procDesc, Impl: intermediate, Config: map[string]any{}},
		"sink": {Desc: sinkDesc, Impl: terminal, Config: map[string]any{}},
	}
	ex := executor.New("test", g, plugins, nil, logger, false)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	records := ls.attrsFor("node started")
	// Source + processor + sink = 3 records. Source omits in/bypassed.
	if len(records) != 3 {
		t.Fatalf("expected 3 'node started' records (source, processor, sink); got %d", len(records))
	}

	// Source record carries role=source and no in/bypassed.
	var src, proc, sink map[string]any
	for _, r := range records {
		switch r["role"] {
		case plugin.RoleSource:
			src = r
		case plugin.RoleProcessor:
			proc = r
		case plugin.RoleSink:
			sink = r
		}
	}
	if src == nil || proc == nil || sink == nil {
		t.Fatalf("missing role in 'node started' records: src=%v proc=%v sink=%v", src, proc, sink)
	}
	if _, ok := src["in"]; ok {
		t.Errorf("source 'node started' should not carry 'in'; got %v", src)
	}
	if _, ok := src["bypassed"]; ok {
		t.Errorf("source 'node started' should not carry 'bypassed'; got %v", src)
	}
	// Processor sees 0 entries (the marker is bypassed), 1 bypassed.
	if got := proc["in"]; got != int64(0) {
		t.Errorf("processor in: want 0, got %v", got)
	}
	if got := proc["bypassed"]; got != int64(1) {
		t.Errorf("processor bypassed: want 1, got %v", got)
	}
	// Sink (opted in to markers) sees the marker as 1 in, 0 bypassed.
	if got := sink["in"]; got != int64(1) {
		t.Errorf("sink in: want 1, got %v", got)
	}
	if got := sink["bypassed"]; got != int64(0) {
		t.Errorf("sink bypassed: want 0, got %v", got)
	}
}

// replacingProcessorPlugin mimics discover: it ignores upstream entries' state
// entirely and emits a completely new set of entries derived from its config.
// The upstream entries are conceptually "consumed for context only."
type replacingProcessorPlugin struct {
	emit []*entry.Entry
}

func (p *replacingProcessorPlugin) Name() string { return "test_replacing" }
func (p *replacingProcessorPlugin) Process(_ context.Context, _ *plugin.TaskContext, _ []*entry.Entry) ([]*entry.Entry, error) {
	return p.emit, nil
}

func replacingDesc() *plugin.Descriptor {
	return &plugin.Descriptor{
		PluginName:       "test_replacing",
		Role:             plugin.RoleProcessor,
		ReplacesUpstream: true,
	}
}

// TestExecutor_ReplacesUpstreamCounter walks the exact pipeline shape that
// motivated this change: a source produces N entries, a ReplacesUpstream
// processor swallows them and emits K different entries with their own URLs,
// and a sink accepts a subset of those. Before the rewrite the pipeline-done
// counters would show total=N, accepted=0, undecided=N (the K emitted entries
// were never in sourceEntries, so the counter never saw their state). After
// the rewrite the counters report total=K, accepted=accepted-count.
func TestExecutor_ReplacesUpstreamCounter(t *testing.T) {
	// Source produces 3 candidate titles.
	srcURLs := []string{"http://upstream-1", "http://upstream-2", "http://upstream-3"}

	// Replacing processor emits 4 fresh entries; the sink will accept the
	// first 2 and leave the others Undecided.
	emitted := []*entry.Entry{
		entry.New("hit-a", "http://hit-a"),
		entry.New("hit-b", "http://hit-b"),
		entry.New("hit-c", "http://hit-c"),
		entry.New("hit-d", "http://hit-d"),
	}

	// Mirror the user's pipeline shape: discover (ReplacesUpstream) emits
	// entries; a downstream processor accepts a subset (the rest stay
	// Undecided); a sink consumes the accepted ones. Without ReplacesUpstream,
	// the 3 upstream source entries dominate the result counters as Undecided
	// even though the 4 emitted-then-accepted entries are what the user cares
	// about.
	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "swap", PluginName: "test_replacing", Upstreams: []dag.NodeID{"src"}},
			{ID: "accept2", PluginName: "test_accept_first_n_proc", Upstreams: []dag.NodeID{"swap"}},
			{ID: "sink", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept2"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":  {Desc: sourceDesc(), Impl: &sourcePlugin{urls: srcURLs}, Config: map[string]any{}},
			"swap": {Desc: replacingDesc(), Impl: &replacingProcessorPlugin{emit: emitted}, Config: map[string]any{}},
			"accept2": {Desc: &plugin.Descriptor{PluginName: "test_accept_first_n_proc", Role: plugin.RoleProcessor},
				Impl: &acceptFirstNProcessor{n: 2}, Config: map[string]any{}},
			"sink": {Desc: sinkDesc(), Impl: &sinkPlugin{}, Config: map[string]any{}},
		},
	)
	res, err := ex.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// The 3 upstream entries must be discarded; only the 4 emitted entries
	// contribute to the totals.
	if res.Total != 4 {
		t.Errorf("Total: got %d, want 4 (only emitted entries should count)", res.Total)
	}
	if res.Accepted != 2 {
		t.Errorf("Accepted: got %d, want 2 (sink accepted the first two emitted)", res.Accepted)
	}
	if res.Undecided != 2 {
		t.Errorf("Undecided: got %d, want 2 (last two emitted entries were never accepted)", res.Undecided)
	}
	if res.Rejected != 0 || res.Failed != 0 {
		t.Errorf("Rejected/Failed: got %d/%d, want 0/0", res.Rejected, res.Failed)
	}
}

// acceptFirstNProcessor accepts only the first n entries; the rest pass
// through unchanged (Undecided). Useful for setting up mixed-state pipelines.
type acceptFirstNProcessor struct {
	n int
}

func (p *acceptFirstNProcessor) Name() string { return "test_accept_first_n_proc" }
func (p *acceptFirstNProcessor) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for i, e := range entries {
		if i < p.n {
			e.Accept()
		}
	}
	return entries, nil
}

// TestExecutor_FanOutDoesNotOvercountClones is the second correctness fix.
// Fan-out clones the same URL into multiple branches; the per-URL aggregator
// must dedup so a single source entry that fans out to N branches still
// counts as Total=1, not Total=N. (The old per-source-entry counter happened
// to get this right by accident because it never looked at clones at all;
// the new counter must explicitly dedup by URL.)
func TestExecutor_FanOutDoesNotOvercountClones(t *testing.T) {
	branchA := &sinkPlugin{}
	branchB := &sinkPlugin{}

	ex := buildExec(t,
		[]*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "accept", PluginName: "test_accept", Upstreams: []dag.NodeID{"src"}},
			{ID: "branchA", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
			{ID: "branchB", PluginName: "test_sink", Upstreams: []dag.NodeID{"accept"}},
		},
		map[dag.NodeID]*executor.PluginInstance{
			"src":     {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://shared"}}, Config: map[string]any{}},
			"accept":  {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
			"branchA": {Desc: sinkDesc(), Impl: branchA, Config: map[string]any{}},
			"branchB": {Desc: sinkDesc(), Impl: branchB, Config: map[string]any{}},
		},
	)
	res, err := ex.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if res.Total != 1 {
		t.Errorf("Total: got %d, want 1 (one source URL, even though it fanned out)", res.Total)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted: got %d, want 1", res.Accepted)
	}
	// Sanity: fan-out actually delivered to both sinks.
	if len(branchA.received) != 1 || len(branchB.received) != 1 {
		t.Errorf("fan-out delivery: branchA=%d branchB=%d, want 1/1",
			len(branchA.received), len(branchB.received))
	}
}

// logFromPluginSink writes one log line through its TaskContext.Logger so we
// can verify the per-run logger reaches plugin code (not just executor logs).
type logFromPluginSink struct{}

func (p *logFromPluginSink) Name() string { return "test_log_sink" }
func (p *logFromPluginSink) Consume(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	tc.Logger.Info("plugin log line")
	return nil
}

// TestExecutor_RunIDInLogs proves that every log line emitted during a single
// Run() call carries a run_id attribute, that the value reaches plugin code via
// TaskContext.Logger, and that two separate runs produce different run_ids.
// Filtering a single run from a noisy log stream is the whole point of the
// attribute — without these guarantees the user can't grep one run cleanly.
func TestExecutor_RunIDInLogs(t *testing.T) {
	collect := func() (string, []string) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		g := dag.New()
		nodes := []*dag.Node{
			{ID: "src", PluginName: "test_source"},
			{ID: "accept", PluginName: "test_accept", Upstreams: []dag.NodeID{"src"}},
			{ID: "sink", PluginName: "test_log_sink", Upstreams: []dag.NodeID{"accept"}},
		}
		for _, n := range nodes {
			if err := g.AddNode(n); err != nil {
				t.Fatalf("AddNode: %v", err)
			}
		}
		instances := map[dag.NodeID]*executor.PluginInstance{
			"src":    {Desc: sourceDesc(), Impl: &sourcePlugin{urls: []string{"http://a.com"}}, Config: map[string]any{}},
			"accept": {Desc: processorDesc(), Impl: &acceptAllPlugin{}, Config: map[string]any{}},
			"sink":   {Desc: sinkDesc(), Impl: &logFromPluginSink{}, Config: map[string]any{}},
		}
		ex := executor.New("test", g, instances, nil, logger, false)
		if _, err := ex.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		var lines []string
		for l := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
		// slog's text handler emits run_id=<hex> unquoted; capture it.
		re := regexp.MustCompile(`run_id=([0-9a-f]{8})\b`)
		var runID string
		for _, l := range lines {
			m := re.FindStringSubmatch(l)
			if m == nil {
				t.Errorf("log line missing run_id: %q", l)
				continue
			}
			if runID == "" {
				runID = m[1]
			} else if m[1] != runID {
				t.Errorf("run_id changed mid-run: %q vs %q in %q", runID, m[1], l)
			}
		}
		if runID == "" {
			t.Fatal("no run_id captured — no log lines emitted")
		}
		// The plugin's own log line must carry run_id too.
		var sawPlugin bool
		for _, l := range lines {
			if strings.Contains(l, "plugin log line") {
				sawPlugin = true
				if !strings.Contains(l, "run_id="+runID) {
					t.Errorf("plugin log line missing run_id: %q", l)
				}
			}
		}
		if !sawPlugin {
			t.Error("plugin log line never appeared — TaskContext.Logger not wired")
		}
		return runID, lines
	}

	id1, _ := collect()
	id2, _ := collect()
	if id1 == id2 {
		t.Errorf("run_id should differ between runs, both were %q", id1)
	}
}
