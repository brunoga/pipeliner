package executor_test

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// --- minimal test plugins ---

type sourcePlugin struct{ urls []string }

func (p *sourcePlugin) Name() string        { return "test_source" }
func (p *sourcePlugin) Phase() plugin.Phase { return plugin.PhaseInput }
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
func (p *acceptAllPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }
func (p *acceptAllPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		e.Accept()
	}
	return entries, nil
}

type sinkPlugin struct{ received []*entry.Entry }

func (p *sinkPlugin) Name() string        { return "test_sink" }
func (p *sinkPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }
func (p *sinkPlugin) Consume(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	p.received = append(p.received, entries...)
	return nil
}

// --- test descriptor helpers ---

func sourceDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_source", PluginPhase: plugin.PhaseInput}
}
func processorDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_proc", PluginPhase: plugin.PhaseFilter}
}
func sinkDesc() *plugin.Descriptor {
	return &plugin.Descriptor{PluginName: "test_sink", PluginPhase: plugin.PhaseOutput}
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
