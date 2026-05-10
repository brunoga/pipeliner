package config

import (
	"fmt"
	"os"
	"path/filepath"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/brunoga/pipeliner/internal/task"
)

// execute runs a Starlark config script and returns a populated Config.
// filename is used for error messages and resolving relative load() paths.
// src is the script source; pass nil to read from filename.
func execute(filename string, src []byte) (*Config, error) {
	ctx := &execContext{
		tasks:     make(map[string][]task.PluginConfig),
		schedules: make(map[string]string),
		dir:       filepath.Dir(filename),
		loaded:    make(map[string]starlark.StringDict),
	}

	thread := &starlark.Thread{
		Name:  filename,
		Load:  ctx.loadModule,
		Print: func(_ *starlark.Thread, msg string) { fmt.Println(msg) },
	}

	predeclared := starlark.StringDict{
		"plugin": starlark.NewBuiltin("plugin", pluginBuiltin),
		"task":   starlark.NewBuiltin("task", ctx.taskBuiltin),
		"env":    starlark.NewBuiltin("env", envBuiltin),
	}

	opts := &syntax.FileOptions{}
	var srcArg interface{} = src
	if src == nil {
		srcArg = nil
	}
	if _, err := starlark.ExecFileOptions(opts, thread, filename, srcArg, predeclared); err != nil {
		return nil, formatStarlarkError(err)
	}

	return &Config{Tasks: ctx.tasks, Schedules: ctx.schedules}, nil
}

// execContext accumulates tasks registered via task() calls during execution.
type execContext struct {
	tasks     map[string][]task.PluginConfig
	schedules map[string]string
	dir       string
	loaded    map[string]starlark.StringDict
}

// loadModule implements load() for the Starlark thread, allowing config files
// to split across multiple .star files: load("./common.star", "helper").
func (ctx *execContext) loadModule(thread *starlark.Thread, module string) (starlark.StringDict, error) {
	// Resolve path relative to the directory of the file being executed.
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

	childThread := &starlark.Thread{
		Name:  abs,
		Load:  ctx.loadModule,
		Print: thread.Print,
	}
	predeclared := starlark.StringDict{
		"plugin": starlark.NewBuiltin("plugin", pluginBuiltin),
		"task":   starlark.NewBuiltin("task", ctx.taskBuiltin),
		"env":    starlark.NewBuiltin("env", envBuiltin),
	}
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, childThread, abs, data, predeclared)
	if err != nil {
		return nil, formatStarlarkError(err)
	}
	ctx.loaded[abs] = globals
	return globals, nil
}

// taskBuiltin implements task(name, plugins, schedule=None, priority=0).
func (ctx *execContext) taskBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		name     string
		plugins  *starlark.List
		schedule starlark.Value = starlark.None
		priority starlark.Value = starlark.MakeInt(0)
	)
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"name", &name,
		"plugins", &plugins,
		"schedule?", &schedule,
		"priority?", &priority,
	); err != nil {
		return nil, err
	}

	var pcs []task.PluginConfig
	for i := 0; i < plugins.Len(); i++ {
		elem := plugins.Index(i)
		pv, ok := elem.(*pluginValue)
		if !ok {
			return nil, fmt.Errorf("task %q: plugins[%d] must be a plugin(...) call, got %s", name, i, elem.Type())
		}
		pcs = append(pcs, task.PluginConfig{Name: pv.pluginName, Config: pv.config})
	}

	ctx.tasks[name] = pcs
	if s, ok := starlark.AsString(schedule); ok && s != "" {
		ctx.schedules[name] = s
	}
	return starlark.None, nil
}

// pluginBuiltin implements plugin(name, config_dict=None, **kwargs).
// Accepts either a dict as the second positional argument (for keys that
// conflict with Starlark syntax, though in practice Starlark has fewer
// reserved words than Python) or keyword arguments.
func pluginBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("plugin: missing name argument")
	}
	name, ok := starlark.AsString(args[0])
	if !ok {
		return nil, fmt.Errorf("plugin: name must be a string, got %s", args[0].Type())
	}
	if len(args) > 2 {
		return nil, fmt.Errorf("plugin: too many positional arguments (expected name and optional dict)")
	}

	cfg := make(map[string]any)

	if len(args) == 2 {
		d, ok := args[1].(*starlark.Dict)
		if !ok {
			return nil, fmt.Errorf("plugin %q: second argument must be a dict, got %s", name, args[1].Type())
		}
		if len(kwargs) > 0 {
			return nil, fmt.Errorf("plugin %q: cannot mix dict argument with keyword arguments", name)
		}
		for _, kv := range d.Items() {
			k, ok := starlark.AsString(kv[0])
			if !ok {
				return nil, fmt.Errorf("plugin %q: config key must be a string", name)
			}
			v, err := toGoValue(kv[1])
			if err != nil {
				return nil, fmt.Errorf("plugin %q key %q: %w", name, k, err)
			}
			cfg[k] = v
		}
	}

	for _, kv := range kwargs {
		k := string(kv[0].(starlark.String))
		v, err := toGoValue(kv[1])
		if err != nil {
			return nil, fmt.Errorf("plugin %q key %q: %w", name, k, err)
		}
		cfg[k] = v
	}

	return &pluginValue{pluginName: name, config: cfg}, nil
}

// envBuiltin implements env(name, default=None).
// Returns the value of the named environment variable. If the variable is not
// set and no default is provided, returns an error. If default is provided,
// returns it when the variable is unset.
func envBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	var def starlark.Value = starlark.None
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"name", &name,
		"default?", &def,
	); err != nil {
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

// pluginValue is the Starlark value returned by plugin(). It carries the plugin
// name and its config map and is collected by task() calls.
type pluginValue struct {
	pluginName string
	config     map[string]any
}

func (p *pluginValue) String() string        { return fmt.Sprintf("plugin(%q)", p.pluginName) }
func (p *pluginValue) Type() string          { return "plugin" }
func (p *pluginValue) Freeze()               {}
func (p *pluginValue) Truth() starlark.Bool  { return starlark.True }
func (p *pluginValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: plugin") }

// toGoValue converts a Starlark value to the equivalent Go value used in
// plugin config maps (map[string]any).
func toGoValue(v starlark.Value) (any, error) {
	switch v := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(v), nil
	case starlark.Int:
		// Return int (not int64) so plugin intVal helpers that switch on
		// `case int:` work without changes. Config integers are always small.
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

// formatStarlarkError produces a human-readable error message from a Starlark
// evaluation error, including the backtrace when available.
func formatStarlarkError(err error) error {
	if evalErr, ok := err.(*starlark.EvalError); ok {
		return fmt.Errorf("%s\n%s", evalErr.Error(), evalErr.Backtrace())
	}
	return err
}
