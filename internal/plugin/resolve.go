package plugin

import (
	"fmt"

	"github.com/brunoga/pipeliner/internal/store"
)

// MakeFromPlugin creates a logging-wrapped InputPlugin from a config item.
// item may be a plugin name string or a map[string]any with a "name" key plus
// config keys. Returns an error if the plugin is not registered under PhaseFrom.
func MakeFromPlugin(item any, db *store.SQLiteStore) (InputPlugin, error) {
	name, cfg, err := ResolveNameAndConfig(item)
	if err != nil {
		return nil, err
	}
	d, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown plugin %q", name)
	}
	if d.PluginPhase != PhaseFrom {
		return nil, fmt.Errorf("plugin %q (phase %q) is not a from plugin; only PhaseFrom plugins may be used in from: lists", name, d.PluginPhase)
	}
	p, err := d.Factory(cfg, db)
	if err != nil {
		return nil, fmt.Errorf("instantiate plugin %q: %w", name, err)
	}
	inp, ok := p.(InputPlugin)
	if !ok {
		return nil, fmt.Errorf("plugin %q does not implement InputPlugin", name)
	}
	return &loggedFromPlugin{inner: inp}, nil
}

// ResolveNameAndConfig extracts a plugin name and config map from either a
// plain string (name only) or a map[string]any with a "name" key.
func ResolveNameAndConfig(item any) (string, map[string]any, error) {
	switch v := item.(type) {
	case string:
		return v, map[string]any{}, nil
	case map[string]any:
		name, ok := v["name"].(string)
		if !ok || name == "" {
			return "", nil, fmt.Errorf("plugin config map must have a non-empty \"name\" field")
		}
		cfg := make(map[string]any, len(v))
		for k, val := range v {
			if k != "name" {
				cfg[k] = val
			}
		}
		return name, cfg, nil
	default:
		return "", nil, fmt.Errorf("plugin config must be a name string or object, got %T", item)
	}
}
