// Package notify defines the Notifier interface and Message type used by the
// notify output plugin. Each notifier implementation (email, webhook, …) is
// registered at startup and selected by name in the plugin config.
package notify

import (
	"context"
	"fmt"
	"sync"

	"github.com/brunoga/pipeliner/internal/entry"
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

// Descriptor holds the factory and optional config validator for a notifier.
type Descriptor struct {
	Factory  Factory
	Validate func(cfg map[string]any) []error // nil means no validation
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
