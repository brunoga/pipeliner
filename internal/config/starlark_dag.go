package config

// starlark_dag.go implements the DAG pipeline built-ins for Starlark config scripts:
//
//	input("plugin-name", key=val, ...)             → nodeHandle (source node)
//	process("plugin-name", upstream=node, key=val, …) → nodeHandle (processor node)
//	merge(node_a, node_b, ...)                      → list of nodeHandles (convenience alias)
//	output("plugin-name", upstream=node, key=val, …)  (sink node; no return value used)
//	pipeline("name", schedule="1h")                 registers accumulated nodes as a named pipeline

import (
	"fmt"

	"go.starlark.net/starlark"

	"github.com/brunoga/pipeliner/internal/dag"
)

// dagNodeRecord holds the information for one pending graph node before
// pipeline() assembles it into a dag.Graph.
type dagNodeRecord struct {
	id         dag.NodeID
	pluginName string
	config     map[string]any
	upstreams  []dag.NodeID
}

// dagGraph is the assembled result: a dag.Graph paired with its raw node
// records (kept for config.Validate to re-inspect plugin names).
type dagGraph struct {
	graph *dag.Graph
	nodes []*dagNodeRecord
}

// nodeHandle is the Starlark value returned by input(), process(), and output().
// It records the NodeID so it can be passed as upstream= to downstream nodes.
type nodeHandle struct {
	id dag.NodeID
}

func (h *nodeHandle) String() string        { return fmt.Sprintf("node(%q)", h.id) }
func (h *nodeHandle) Type() string          { return "node" }
func (h *nodeHandle) Freeze()               {}
func (h *nodeHandle) Truth() starlark.Bool  { return starlark.True }
func (h *nodeHandle) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: node") }

// nextNodeID generates a unique, readable NodeID like "rss_0", "seen_1".
func (ctx *execContext) nextNodeID(pluginName string) dag.NodeID {
	id := dag.NodeID(fmt.Sprintf("%s_%d", pluginName, ctx.nodeCounter))
	ctx.nodeCounter++
	return id
}

// resolveFrom converts a upstream= argument (nodeHandle or list of nodeHandles)
// into a slice of NodeIDs.
func resolveFrom(v starlark.Value) ([]dag.NodeID, error) {
	switch v := v.(type) {
	case *nodeHandle:
		return []dag.NodeID{v.id}, nil
	case *starlark.List:
		ids := make([]dag.NodeID, v.Len())
		for i := 0; i < v.Len(); i++ {
			h, ok := v.Index(i).(*nodeHandle)
			if !ok {
				return nil, fmt.Errorf("upstream= list element %d must be a node handle, got %s", i, v.Index(i).Type())
			}
			ids[i] = h.id
		}
		return ids, nil
	case starlark.NoneType:
		return nil, nil
	default:
		return nil, fmt.Errorf("upstream= must be a node handle or list of node handles, got %s", v.Type())
	}
}

// inputBuiltin implements input("plugin-name", key=val, ...) → nodeHandle.
func (ctx *execContext) inputBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s: missing plugin name", fn.Name())
	}
	name, ok := starlark.AsString(args[0])
	if !ok {
		return nil, fmt.Errorf("%s: plugin name must be a string", fn.Name())
	}
	cfg, err := kwargsToConfig(name, kwargs)
	if err != nil {
		return nil, err
	}
	id := ctx.nextNodeID(name)
	ctx.pendingNodes = append(ctx.pendingNodes, &dagNodeRecord{
		id:         id,
		pluginName: name,
		config:     cfg,
	})
	return &nodeHandle{id: id}, nil
}

// processBuiltin implements process("plugin-name", upstream=node, key=val, ...) → nodeHandle.
func (ctx *execContext) processBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s: missing plugin name", fn.Name())
	}
	name, ok := starlark.AsString(args[0])
	if !ok {
		return nil, fmt.Errorf("%s: plugin name must be a string", fn.Name())
	}
	fromVal, cfg, err := extractFromAndConfig(name, fn.Name(), kwargs)
	if err != nil {
		return nil, err
	}
	upstreams, err := resolveFrom(fromVal)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", fn.Name(), name, err)
	}
	id := ctx.nextNodeID(name)
	ctx.pendingNodes = append(ctx.pendingNodes, &dagNodeRecord{
		id:         id,
		pluginName: name,
		config:     cfg,
		upstreams:  upstreams,
	})
	return &nodeHandle{id: id}, nil
}

// mergeBuiltin implements merge(node_a, node_b, ...) → starlark.List of nodeHandles.
// It is a convenience alias: merging two nodes is equivalent to passing a list
// as upstream= to the next process() call. No graph node is created.
func (ctx *execContext) mergeBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("%s: does not accept keyword arguments", fn.Name())
	}
	if len(args) < 2 {
		return nil, fmt.Errorf("%s: requires at least 2 node handles", fn.Name())
	}
	elems := make([]starlark.Value, len(args))
	for i, a := range args {
		if _, ok := a.(*nodeHandle); !ok {
			return nil, fmt.Errorf("%s: argument %d must be a node handle, got %s", fn.Name(), i, a.Type())
		}
		elems[i] = a
	}
	return starlark.NewList(elems), nil
}

// outputBuiltin implements output("plugin-name", upstream=node, key=val, ...).
// Returns None (sinks have no downstream).
func (ctx *execContext) outputBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s: missing plugin name", fn.Name())
	}
	name, ok := starlark.AsString(args[0])
	if !ok {
		return nil, fmt.Errorf("%s: plugin name must be a string", fn.Name())
	}
	fromVal, cfg, err := extractFromAndConfig(name, fn.Name(), kwargs)
	if err != nil {
		return nil, err
	}
	upstreams, err := resolveFrom(fromVal)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", fn.Name(), name, err)
	}
	id := ctx.nextNodeID(name)
	ctx.pendingNodes = append(ctx.pendingNodes, &dagNodeRecord{
		id:         id,
		pluginName: name,
		config:     cfg,
		upstreams:  upstreams,
	})
	return starlark.None, nil
}

// pipelineBuiltin implements pipeline("name", schedule="...").
// Assembles all pending nodes into a dag.Graph and registers it.
func (ctx *execContext) pipelineBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		name     string
		schedule starlark.Value = starlark.None
	)
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"name", &name,
		"schedule?", &schedule,
	); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("%s: name must not be empty", fn.Name())
	}
	if len(ctx.pendingNodes) == 0 {
		return nil, fmt.Errorf("%s %q: no nodes defined before pipeline() call", fn.Name(), name)
	}

	g := dag.New()
	for _, rec := range ctx.pendingNodes {
		if err := g.AddNode(&dag.Node{
			ID:         rec.id,
			PluginName: rec.pluginName,
			Config:     rec.config,
			Upstreams:  rec.upstreams,
		}); err != nil {
			return nil, fmt.Errorf("%s %q: %w", fn.Name(), name, err)
		}
	}

	ctx.graphs[name] = &dagGraph{graph: g, nodes: ctx.pendingNodes}
	ctx.pendingNodes = nil // reset for the next pipeline() block

	if s, ok := starlark.AsString(schedule); ok && s != "" {
		ctx.graphSchedules[name] = s
	}
	return starlark.None, nil
}

// extractFromAndConfig splits kwargs into the upstream= value and the remaining
// plugin config. The upstream= kwarg is consumed; all others are passed to
// kwargsToConfig.
func extractFromAndConfig(pluginName string, _ string, kwargs []starlark.Tuple) (fromVal starlark.Value, cfg map[string]any, err error) {
	fromVal = starlark.None
	var remaining []starlark.Tuple
	for _, kv := range kwargs {
		if string(kv[0].(starlark.String)) == "upstream" {
			fromVal = kv[1]
		} else {
			remaining = append(remaining, kv)
		}
	}
	cfg, err = kwargsToConfig(pluginName, remaining)
	return fromVal, cfg, err
}

// kwargsToConfig converts Starlark keyword arguments into a plugin config map.
func kwargsToConfig(pluginName string, kwargs []starlark.Tuple) (map[string]any, error) {
	cfg := make(map[string]any, len(kwargs))
	for _, kv := range kwargs {
		k := string(kv[0].(starlark.String))
		v, err := toGoValue(kv[1])
		if err != nil {
			return nil, fmt.Errorf("plugin %q key %q: %w", pluginName, k, err)
		}
		cfg[k] = v
	}
	return cfg, nil
}
