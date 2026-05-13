package config

// starlark_dag.go implements the DAG pipeline built-ins for Starlark config scripts:
//
//	input("plugin-name", key=val, ...)                → nodeHandle (source node)
//	process("plugin-name", upstream=node, key=val, …) → nodeHandle (processor node)
//	merge(node_a, node_b, ...)                         → list of nodeHandles (convenience alias)
//	output("plugin-name", upstream=node, key=val, …)  → nodeHandle (sink node)
//	pipeline("name", schedule="1h")                    registers accumulated nodes as a named pipeline
//
// output() returns a nodeHandle that can be passed as upstream= to another output()
// call, enabling sink chaining (output → output). Only sink → sink connections are
// valid; connecting an output's handle to a process() call is rejected by Validate.

import (
	"fmt"
	"strings"

	"go.starlark.net/starlark"

	"github.com/brunoga/pipeliner/internal/dag"
)

// activeFunctionCall returns the call key for the innermost user function on
// the Starlark call stack, or "" if none. The key is "funcName@line:col" where
// line:col is the position of the call to the user function in its outer scope,
// making it unique per call site.
func (ctx *execContext) activeFunctionCall(thread *starlark.Thread) string {
	depth := thread.CallStackDepth()
	// Depth 0 = the current built-in (process/input/output).
	// Depth 1 = its direct caller (a user function or <toplevel>).
	for i := 1; i < depth; i++ {
		frame := thread.CallFrame(i)
		if _, ok := ctx.userFunctions[frame.Name]; ok {
			// The frame at i+1 is the scope that called the user function; its
			// Position is the call site that uniquely identifies this invocation.
			if i+1 < depth {
				outer := thread.CallFrame(i + 1)
				return fmt.Sprintf("%s@%d:%d", frame.Name, outer.Pos.Line, outer.Pos.Col)
			}
			return fmt.Sprintf("%s@%d:%d", frame.Name, frame.Pos.Line, frame.Pos.Col)
		}
	}
	return ""
}

// dagNodeRecord holds the information for one pending graph node before
// pipeline() assembles it into a dag.Graph.
type dagNodeRecord struct {
	id              dag.NodeID
	pluginName      string
	config          map[string]any
	upstreams       []dag.NodeID
	functionCallKey string // non-empty when created inside a user function call
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
func (ctx *execContext) inputBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
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
		id:              id,
		pluginName:      name,
		config:          cfg,
		functionCallKey: ctx.activeFunctionCall(thread),
	})
	return &nodeHandle{id: id}, nil
}

// processBuiltin implements process("plugin-name", upstream=node, key=val, ...) → nodeHandle.
func (ctx *execContext) processBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
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
		id:              id,
		pluginName:      name,
		config:          cfg,
		upstreams:       upstreams,
		functionCallKey: ctx.activeFunctionCall(thread),
	})
	return &nodeHandle{id: id}, nil
}

// mergeBuiltin implements merge(node_a, node_b, ...) → starlark.List of nodeHandles.
// It is a convenience alias: merging two nodes is equivalent to passing a list
// as upstream= to the next process() call. No graph node is created.
func (ctx *execContext) mergeBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) { //nolint:unparam
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

// outputBuiltin implements output("plugin-name", upstream=node, key=val, ...) → nodeHandle.
// Returns a nodeHandle that can be passed as upstream= to another output() call,
// enabling sink chaining. Only sink → sink connections are valid in the DAG validator.
func (ctx *execContext) outputBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
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
		id:              id,
		pluginName:      name,
		config:          cfg,
		upstreams:       upstreams,
		functionCallKey: ctx.activeFunctionCall(thread),
	})
	return &nodeHandle{id: id}, nil
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

	// Build FunctionCallRecords by grouping nodes that share the same call key.
	callMap := make(map[string]*FunctionCallRecord)
	callOrder := make([]string, 0) // preserve first-seen order
	for _, rec := range ctx.pendingNodes {
		if rec.functionCallKey == "" {
			continue
		}
		fcr, exists := callMap[rec.functionCallKey]
		if !exists {
			funcName, _, _ := strings.Cut(rec.functionCallKey, "@")
			fcr = &FunctionCallRecord{
				CallKey:  rec.functionCallKey,
				FuncName: funcName,
				Args:     make(map[string]any),
			}
			callMap[rec.functionCallKey] = fcr
			callOrder = append(callOrder, rec.functionCallKey)
		}
		fcr.InternalNodeIDs = append(fcr.InternalNodeIDs, string(rec.id))
		fcr.ReturnNodeID = string(rec.id) // last node encountered = return node
	}
	for _, key := range callOrder {
		ctx.functionCalls[name] = append(ctx.functionCalls[name], callMap[key])
	}

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
