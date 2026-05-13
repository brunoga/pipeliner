package config

import (
	"fmt"
	"os"
	"path/filepath"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/brunoga/pipeliner/internal/dag"
)

// execute runs a Starlark config script and returns a populated Config.
func execute(filename string, src []byte) (*Config, error) {
	ctx := &execContext{
		graphs:         make(map[string]*dagGraph),
		graphSchedules: make(map[string]string),
		dir:            filepath.Dir(filename),
		loaded:         make(map[string]starlark.StringDict),
	}

	thread := &starlark.Thread{
		Name:  filename,
		Load:  ctx.loadModule,
		Print: func(_ *starlark.Thread, msg string) { fmt.Println(msg) },
	}

	predeclared := ctx.predeclared()
	opts := &syntax.FileOptions{}
	var srcArg any = src
	if src == nil {
		srcArg = nil
	}
	if _, err := starlark.ExecFileOptions(opts, thread, filename, srcArg, predeclared); err != nil {
		return nil, formatStarlarkError(err)
	}

	graphs := make(map[string]*dag.Graph, len(ctx.graphs))
	for name, dg := range ctx.graphs {
		graphs[name] = dg.graph
	}
	return &Config{
		Graphs:         graphs,
		GraphSchedules: ctx.graphSchedules,
	}, nil
}

// execContext accumulates DAG pipeline graphs registered during Starlark execution.
type execContext struct {
	graphs         map[string]*dagGraph
	graphSchedules map[string]string
	pendingNodes   []*dagNodeRecord
	nodeCounter    int
	dir            string
	loaded         map[string]starlark.StringDict
}

// predeclared returns the built-in functions available to config scripts.
func (ctx *execContext) predeclared() starlark.StringDict {
	return starlark.StringDict{
		"input":    starlark.NewBuiltin("input", ctx.inputBuiltin),
		"process":  starlark.NewBuiltin("process", ctx.processBuiltin),
		"merge":    starlark.NewBuiltin("merge", ctx.mergeBuiltin),
		"output":   starlark.NewBuiltin("output", ctx.outputBuiltin),
		"pipeline": starlark.NewBuiltin("pipeline", ctx.pipelineBuiltin),
		"env":      starlark.NewBuiltin("env", envBuiltin),
	}
}

func (ctx *execContext) loadModule(thread *starlark.Thread, module string) (starlark.StringDict, error) {
	abs := module
	if !filepath.IsAbs(module) {
		abs = filepath.Join(ctx.dir, module)
	}
	if cached, ok := ctx.loaded[abs]; ok {
		return cached, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("load %q: %w", module, err)
	}
	childThread := &starlark.Thread{Name: abs, Load: ctx.loadModule, Print: thread.Print}
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, childThread, abs, data, ctx.predeclared())
	if err != nil {
		return nil, formatStarlarkError(err)
	}
	ctx.loaded[abs] = globals
	return globals, nil
}

// envBuiltin implements env(name, default=None).
func envBuiltin(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	var def starlark.Value = starlark.None
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "name", &name, "default?", &def); err != nil {
		return nil, err
	}
	val, set := os.LookupEnv(name)
	if set {
		return starlark.String(val), nil
	}
	if def != starlark.None {
		return def, nil
	}
	return nil, fmt.Errorf("env: environment variable %q is not set", name)
}

// toGoValue converts a Starlark value to the equivalent Go value used in plugin config maps.
func toGoValue(v starlark.Value) (any, error) {
	switch v := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(v), nil
	case starlark.Int:
		n, ok := v.Int64()
		if !ok {
			return nil, fmt.Errorf("integer value out of int64 range")
		}
		return int(n), nil
	case starlark.Float:
		return float64(v), nil
	case starlark.String:
		return string(v), nil
	case *starlark.List:
		items := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			item, err := toGoValue(v.Index(i))
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			items[i] = item
		}
		return items, nil
	case starlark.Tuple:
		items := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			item, err := toGoValue(v.Index(i))
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			items[i] = item
		}
		return items, nil
	case *starlark.Dict:
		m := make(map[string]any, v.Len())
		for _, kv := range v.Items() {
			k, ok := starlark.AsString(kv[0])
			if !ok {
				return nil, fmt.Errorf("dict key must be a string, got %s", kv[0].Type())
			}
			val, err := toGoValue(kv[1])
			if err != nil {
				return nil, fmt.Errorf("[%q]: %w", k, err)
			}
			m[k] = val
		}
		return m, nil
	default:
		return nil, fmt.Errorf("unsupported Starlark type %s", v.Type())
	}
}

func formatStarlarkError(err error) error {
	if evalErr, ok := err.(*starlark.EvalError); ok {
		return fmt.Errorf("%s\n%s", evalErr.Error(), evalErr.Backtrace())
	}
	return err
}
