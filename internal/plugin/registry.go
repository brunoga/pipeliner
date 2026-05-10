package plugin

import (
	"fmt"
	"sort"
	"sync"

	"github.com/brunoga/pipeliner/internal/store"
)

// Factory creates a new Plugin instance from a configuration block.
// db is the shared store for the current config; plugins that need persistent
// state use it directly rather than opening their own connection.
type Factory func(cfg map[string]any, db *store.SQLiteStore) (Plugin, error)

// Descriptor holds metadata about a registered plugin type.
type Descriptor struct {
	PluginName  string
	Description string
	// PluginPhase is the legacy phase identifier used by the linear task engine.
	// New plugins should set Role instead; existing plugins may set both.
	PluginPhase Phase
	// Role is the DAG role for this plugin. If empty, EffectiveRole() derives
	// it from PluginPhase for backward compatibility.
	Role Role
	// Produces lists entry field names this plugin writes to Fields.
	// Used by the DAG validator to check that downstream nodes' Requires are met.
	Produces []string
	// Requires lists entry field names this plugin reads from Fields.
	// The DAG validator ensures at least one upstream node Produces each field.
	Requires []string
	Factory  Factory
	// Validate checks the plugin's configuration map and returns any errors.
	// It is called by config.Validate before plugin construction so all
	// config errors are surfaced at once by pipeliner check.
	// nil means no validation beyond what the factory enforces.
	Validate func(cfg map[string]any) []error
	// Schema declares the config keys accepted by this plugin. It is optional
	// but enables typed form fields in the visual pipeline editor. Plugins
	// without a Schema get a generic key-value editor instead.
	Schema []FieldSchema
}

// EffectiveRole returns the plugin's Role, deriving it from PluginPhase when
// Role is not explicitly set. This allows legacy plugins registered with only
// PluginPhase to participate in DAG-based pipelines without modification.
func (d *Descriptor) EffectiveRole() Role {
	if d.Role != "" {
		return d.Role
	}
	switch d.PluginPhase {
	case PhaseInput:
		return RoleSource
	case PhaseMetainfo, PhaseFilter, PhaseModify:
		return RoleProcessor
	case PhaseOutput, PhaseLearn:
		return RoleSink
	case PhaseFrom:
		return RoleSource
	default:
		return RoleProcessor
	}
}

var (
	mu       sync.RWMutex
	registry = map[string]*Descriptor{}
)

// Register adds a plugin descriptor to the global registry.
// It panics if a plugin with the same name is already registered.
func Register(d *Descriptor) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[d.PluginName]; exists {
		panic(fmt.Sprintf("plugin %q already registered", d.PluginName))
	}
	registry[d.PluginName] = d
}

// Lookup returns the descriptor for the named plugin, or (nil, false) if not found.
func Lookup(name string) (*Descriptor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := registry[name]
	return d, ok
}

// All returns all registered descriptors sorted by name.
func All() []*Descriptor {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Descriptor, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PluginName < out[j].PluginName })
	return out
}


