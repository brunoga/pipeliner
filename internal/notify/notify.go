// Package notify defines the Notifier interface and Message type used by the
// notify output plugin. Each notifier implementation (email, webhook, …) is
// registered at startup and selected by name in the plugin config.
package notify

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// Message is the payload sent to a Notifier.
type Message struct {
	Title   string
	Body    string
	Entries []*entry.Entry
}

// Notifier sends a notification message.
type Notifier interface {
	Send(ctx context.Context, msg Message) error
}

// Factory creates a Notifier from a config map.
type Factory func(cfg map[string]any) (Notifier, error)

// Descriptor holds the factory, optional config validator, and the config
// schema for a notifier. The Schema drives the visual editor: when a notify
// node selects this notifier via `via=`, the editor renders these fields
// (bound to the notify node's nested `config={}` dict) so credentials and
// other settings are editable through the UI, not only the text config.
type Descriptor struct {
	Factory  Factory
	Validate func(cfg map[string]any) []error // nil means no validation
	Schema   []plugin.FieldSchema             // config keys, for the visual editor
}

// Registered pairs a notifier name with its descriptor.
type Registered struct {
	Name string
	Descriptor
}

var (
	mu       sync.RWMutex
	registry = map[string]Descriptor{}
)

// Register adds a named notifier descriptor. Panics on duplicate name.
func Register(name string, d Descriptor) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[name]; ok {
		panic(fmt.Sprintf("notify: duplicate notifier %q", name))
	}
	registry[name] = d
}

// Lookup returns the descriptor for name, or (zero, false) if not registered.
func Lookup(name string) (Descriptor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := registry[name]
	return d, ok
}

// All returns every registered notifier, sorted by name. Used by the web
// layer to expose notifier schemas to the visual editor.
func All() []Registered {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Registered, 0, len(registry))
	for name, d := range registry {
		out = append(out, Registered{Name: name, Descriptor: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
