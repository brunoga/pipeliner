package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/store"
)

// NodePipelineStep is one plugin in a mini-pipeline, in topological order.
type NodePipelineStep struct {
	PluginName string
	Config     map[string]any
}

// NodePipeline is a mini-pipeline that can be used as a list= or search= item.
// It is produced during config parsing when a nodeHandle is passed as a list/search item.
// Steps are in topological order (source first, processors after).
type NodePipeline struct {
	Steps []NodePipelineStep
}

// MakeListPipeline instantiates a NodePipeline as a SourcePlugin.
// The first step must have RoleSource; all subsequent steps must have RoleProcessor.
func MakeListPipeline(p *NodePipeline, db *store.SQLiteStore) (SourcePlugin, error) {
	if len(p.Steps) == 0 {
		return nil, fmt.Errorf("mini-pipeline: empty pipeline")
	}
	src, procs, name, err := instantiatePipeline(p, db)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("mini-pipeline %q: first step must be a source plugin", name)
	}
	return &miniPipelineSource{source: src, procs: procs, name: name}, nil
}

// MakeSearchPipeline instantiates a NodePipeline as a SearchPlugin.
// The first step must have IsSearchPlugin=true; all subsequent steps must have RoleProcessor.
func MakeSearchPipeline(p *NodePipeline, db *store.SQLiteStore) (SearchPlugin, error) {
	if len(p.Steps) == 0 {
		return nil, fmt.Errorf("mini-pipeline: empty pipeline")
	}
	first := p.Steps[0]
	d, ok := Lookup(first.PluginName)
	if !ok {
		return nil, fmt.Errorf("mini-pipeline: unknown plugin %q", first.PluginName)
	}
	if !d.IsSearchPlugin {
		return nil, fmt.Errorf("mini-pipeline: first step %q must be a search plugin (IsSearchPlugin=true)", first.PluginName)
	}

	plug, err := d.Factory(first.Config, db)
	if err != nil {
		return nil, fmt.Errorf("mini-pipeline: instantiate %q: %w", first.PluginName, err)
	}
	root, ok := plug.(SearchPlugin)
	if !ok {
		return nil, fmt.Errorf("mini-pipeline: %q does not implement SearchPlugin", first.PluginName)
	}

	names := []string{first.PluginName}
	var procs []ProcessorPlugin
	for _, step := range p.Steps[1:] {
		proc, err := instantiateProcessor(step, db)
		if err != nil {
			return nil, err
		}
		procs = append(procs, proc)
		names = append(names, step.PluginName)
	}
	return &miniPipelineSearch{root: root, procs: procs, name: strings.Join(names, "→")}, nil
}

// instantiatePipeline creates the source + processor chain for a list mini-pipeline.
func instantiatePipeline(p *NodePipeline, db *store.SQLiteStore) (SourcePlugin, []ProcessorPlugin, string, error) {
	first := p.Steps[0]
	d, ok := Lookup(first.PluginName)
	if !ok {
		return nil, nil, first.PluginName, fmt.Errorf("mini-pipeline: unknown plugin %q", first.PluginName)
	}
	if d.EffectiveRole() != RoleSource {
		return nil, nil, first.PluginName, fmt.Errorf("mini-pipeline: first step %q must be a source plugin, got role %q", first.PluginName, d.EffectiveRole())
	}
	plug, err := d.Factory(first.Config, db)
	if err != nil {
		return nil, nil, first.PluginName, fmt.Errorf("mini-pipeline: instantiate %q: %w", first.PluginName, err)
	}
	src, ok := plug.(SourcePlugin)
	if !ok {
		return nil, nil, first.PluginName, fmt.Errorf("mini-pipeline: %q does not implement SourcePlugin", first.PluginName)
	}

	names := []string{first.PluginName}
	var procs []ProcessorPlugin
	for _, step := range p.Steps[1:] {
		proc, err := instantiateProcessor(step, db)
		if err != nil {
			return nil, nil, "", err
		}
		procs = append(procs, proc)
		names = append(names, step.PluginName)
	}
	return src, procs, strings.Join(names, "→"), nil
}

func instantiateProcessor(step NodePipelineStep, db *store.SQLiteStore) (ProcessorPlugin, error) {
	d, ok := Lookup(step.PluginName)
	if !ok {
		return nil, fmt.Errorf("mini-pipeline: unknown plugin %q", step.PluginName)
	}
	if d.EffectiveRole() != RoleProcessor {
		return nil, fmt.Errorf("mini-pipeline: step %q must be a processor plugin, got role %q", step.PluginName, d.EffectiveRole())
	}
	plug, err := d.Factory(step.Config, db)
	if err != nil {
		return nil, fmt.Errorf("mini-pipeline: instantiate %q: %w", step.PluginName, err)
	}
	proc, ok := plug.(ProcessorPlugin)
	if !ok {
		return nil, fmt.Errorf("mini-pipeline: %q does not implement ProcessorPlugin", step.PluginName)
	}
	return proc, nil
}

// miniPipelineSource implements SourcePlugin by running a source followed by
// zero or more processors. CommitPlugin is intentionally not implemented —
// mini-pipelines are used as title sources and must not persist state.
type miniPipelineSource struct {
	source SourcePlugin
	procs  []ProcessorPlugin
	name   string
}

func (m *miniPipelineSource) Name()     string { return m.name }
func (m *miniPipelineSource) CacheKey() string { return "mini:" + m.name }

func (m *miniPipelineSource) Generate(ctx context.Context, tc *TaskContext) ([]*entry.Entry, error) {
	entries, err := m.source.Generate(ctx, tc)
	if err != nil {
		return nil, fmt.Errorf("mini-pipeline %q: source: %w", m.name, err)
	}
	return m.runProcessors(ctx, tc, entries)
}

func (m *miniPipelineSource) runProcessors(ctx context.Context, tc *TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, proc := range m.procs {
		var err error
		entries, err = proc.Process(ctx, tc, entries)
		if err != nil {
			return nil, fmt.Errorf("mini-pipeline %q: processor %q: %w", m.name, proc.Name(), err)
		}
		entries = entry.PassThrough(entries)
	}
	return entries, nil
}

// miniPipelineSearch implements SearchPlugin by running a search plugin followed
// by zero or more processors.
type miniPipelineSearch struct {
	root  SearchPlugin
	procs []ProcessorPlugin
	name  string
}

func (m *miniPipelineSearch) Name() string { return m.name }

func (m *miniPipelineSearch) Search(ctx context.Context, tc *TaskContext, query string) ([]*entry.Entry, error) {
	entries, err := m.root.Search(ctx, tc, query)
	if err != nil {
		return nil, fmt.Errorf("mini-pipeline %q: search: %w", m.name, err)
	}
	for _, proc := range m.procs {
		entries, err = proc.Process(ctx, tc, entries)
		if err != nil {
			return nil, fmt.Errorf("mini-pipeline %q: processor %q: %w", m.name, proc.Name(), err)
		}
		entries = entry.PassThrough(entries)
	}
	return entries, nil
}
