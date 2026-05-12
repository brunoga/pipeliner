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
	// Role declares the plugin's place in a DAG pipeline.
	Role Role
	// Produces lists entry field names this plugin writes to Fields.
	// Used by the DAG validator to check that downstream nodes' Requires are met.
	Produces []string
	// Requires lists entry field names this plugin reads from Fields.
	// The DAG validator ensures at least one upstream node Produces each field.
	Requires []string
	Factory  Factory
	// Validate checks the plugin's configuration map and returns any errors.
	// Called by config.Validate before plugin construction.
	// nil means no validation beyond what the factory enforces.
	Validate func(cfg map[string]any) []error
	// Schema declares the config keys accepted by this plugin. Enables typed
	// form fields in the visual pipeline editor.
	Schema []FieldSchema
	// AcceptsVia indicates this plugin takes a "via" list of search sub-plugin
	// configs (e.g. discover). The visual editor shows a dedicated bottom port
	// for these connections and serialises them inline as via=[{...}, ...].
	AcceptsVia bool
	// IsSearchPlugin marks plugins that implement the SearchPlugin interface
	// and can therefore be used in a discover via=[...] list. Only these
	// plugins may be dropped onto a via-port in the visual editor.
	IsSearchPlugin bool
	// AcceptsFrom indicates the plugin takes a "from" list of source-plugin
	// configs that supply a dynamic title/name list (e.g. series, movies).
	// The visual editor shows a dedicated teal port for these connections.
	AcceptsFrom bool
	// IsFromPlugin marks source plugins whose entry titles can be used as a
	// name list by AcceptsFrom plugins (e.g. tvdb_favorites, trakt_list).
	IsFromPlugin bool
}

// EffectiveRole returns the plugin's Role.
func (d *Descriptor) EffectiveRole() Role {
	if d.Role != "" {
		return d.Role
	}
	return RoleProcessor // safe default
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


