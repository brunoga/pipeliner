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

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register adds a named notifier factory. Panics on duplicate name.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[name]; ok {
		panic(fmt.Sprintf("notify: duplicate notifier %q", name))
	}
	registry[name] = f
}

// Lookup returns the factory for name, or (nil, false) if not registered.
func Lookup(name string) (Factory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	f, ok := registry[name]
	return f, ok
}
