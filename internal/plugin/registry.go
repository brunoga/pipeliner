package plugin

import (
	"fmt"
	"sort"
	"sync"

	"github.com/brunoga/pipeliner/internal/entry"
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
	// Produces lists entry field names this plugin writes to Fields on every
	// passing entry. Used by the DAG validator for field reachability checks.
	Produces []string
	// MayProduce lists fields this plugin sets only on some entries (e.g. when
	// parsing succeeds or a lookup finds a match). The DAG validator treats
	// these as reachable but emits a warning when a downstream Requires group
	// can only be satisfied by MayProduce or merge-partial fields.
	MayProduce []string
	// Requires lists groups of entry field names this plugin reads from Fields.
	// Each inner slice is an OR group: at least one field in the group must be
	// produced by a transitive upstream. Groups are ANDed: every group must be
	// independently satisfied. Use RequireAll for AND chains and RequireAny for
	// OR groups.
	Requires [][]string
	Factory  Factory
	// Validate checks the plugin's configuration map and returns any errors.
	// Called by config.Validate before plugin construction.
	// nil means no validation beyond what the factory enforces.
	Validate func(cfg map[string]any) []error
	// Schema declares the config keys accepted by this plugin. Enables typed
	// form fields in the visual pipeline editor.
	Schema []FieldSchema
	// AcceptsSearch indicates this plugin takes a "search" list of search sub-plugin
	// configs (e.g. discover). The visual editor shows a dedicated bottom port
	// for these connections and serialises them inline as search=[{...}, ...].
	AcceptsSearch bool
	// IsSearchPlugin marks plugins that implement the SearchPlugin interface
	// and can therefore be used in a discover search=[...] list. Only these
	// plugins may be dropped onto a search-port in the visual editor.
	IsSearchPlugin bool
	// AcceptsList indicates the plugin takes a "list" list of source-plugin
	// configs that supply a dynamic title/name list (e.g. series, movies).
	// The visual editor shows a dedicated teal port for these connections.
	AcceptsList bool
	// IsListPlugin marks source plugins whose entry titles can be used as a
	// name list by AcceptsList plugins (e.g. tvdb_favorites, trakt_list).
	IsListPlugin bool
	// Internal marks plugins that are implementation details of a builtin
	// (e.g. route_selector created by the route() Starlark builtin). Internal
	// plugins are registered so the executor can instantiate them but are hidden
	// from the visual editor palette and cannot be used directly in config.
	Internal bool
	// InputStates declares which entry states the plugin's Process or Consume
	// method actually acts on. The executor pre-filters upstream entries to
	// this set before calling the plugin, removing the per-plugin
	// "skip rejected/failed" boilerplate that lived at the top of every
	// processor and the FilterAccepted wrapper that lived at the top of every
	// sink. Excluded entries bypass the plugin and are merged back into the
	// downstream slice unchanged.
	//
	// When zero, EffectiveInputStates falls back to a role-appropriate default:
	//   - RoleProcessor → entry.StatesAcceptedUndecided
	//   - RoleSink      → entry.StatesAcceptedOnly
	//
	// Sinks additionally have an always-on consumed-exclusion at the boundary,
	// regardless of the declared InputStates — consumed is orthogonal to State
	// and the executor's SplitConsumed pass handles it independently.
	//
	// Plugins with non-default needs declare an explicit set; see the
	// pre-built named constants in package entry.
	InputStates entry.StateSet
}

// EffectiveRole returns the plugin's Role.
func (d *Descriptor) EffectiveRole() Role {
	if d.Role != "" {
		return d.Role
	}
	return RoleProcessor // safe default
}

// EffectiveInputStates returns the StateSet the executor should pre-filter
// upstream entries to before calling this plugin's Process or Consume
// method. When InputStates is unset (zero), a role-appropriate default is
// applied:
//
//   - RoleProcessor → entry.StatesAcceptedUndecided  (matches the legacy
//     per-plugin skip-guard convention)
//   - RoleSink      → entry.StatesAcceptedOnly       (sinks act only on
//     accepted entries; the consumed flag is excluded by a separate
//     always-on filter at the sink boundary in the executor)
//   - RoleSource    → entry.StatesAll                (sources don't consume
//     input; the value is irrelevant)
//
// Sinks that legitimately need broader access — e.g. a notify backend that
// reports on failed entries from an upstream sink — declare an explicit
// non-default set, just like processors do.
func (d *Descriptor) EffectiveInputStates() entry.StateSet {
	if d.InputStates != 0 {
		return d.InputStates
	}
	switch d.EffectiveRole() {
	case RoleProcessor:
		return entry.StatesAcceptedUndecided
	case RoleSink:
		return entry.StatesAcceptedOnly
	}
	return entry.StatesAll
}

// RequiresFlat returns a deduplicated flat list of all fields mentioned in any
// Requires group. Useful for display and backward-compatible JSON APIs that
// cannot express OR-group semantics.
func (d *Descriptor) RequiresFlat() []string {
	seen := make(map[string]bool)
	var out []string
	for _, group := range d.Requires {
		for _, f := range group {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
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
