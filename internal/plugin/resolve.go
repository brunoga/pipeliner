package plugin

import (
	"fmt"

	"github.com/brunoga/pipeliner/internal/store"
)

// MakeListPlugin creates a logging-wrapped SourcePlugin from a config item.
// item may be a plugin name string, a map[string]any with a "name" key, or a
// *NodePipeline (produced when a nodeHandle is passed as a list= item in config).
// The plugin must have Role=RoleSource.
func MakeListPlugin(item any, db *store.SQLiteStore) (SourcePlugin, error) {
	if p, ok := item.(*NodePipeline); ok {
		return MakeListPipeline(p, db)
	}
	name, cfg, err := ResolveNameAndConfig(item)
	if err != nil {
		return nil, err
	}
	d, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown plugin %q", name)
	}
	if d.EffectiveRole() != RoleSource {
		return nil, fmt.Errorf("plugin %q (role %q) is not a source plugin; only source plugins may be used in list=", name, d.EffectiveRole())
	}
	p, err := d.Factory(cfg, db)
	if err != nil {
		return nil, fmt.Errorf("instantiate plugin %q: %w", name, err)
	}
	src, ok := p.(SourcePlugin)
	if !ok {
		return nil, fmt.Errorf("plugin %q does not implement SourcePlugin", name)
	}
	return &loggedSourcePlugin{inner: src}, nil
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
