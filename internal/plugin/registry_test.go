package plugin

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/store"
)

// resetForTest clears the global registry between test cases.
func resetForTest() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]*Descriptor{}
}

// stubInput is a minimal InputPlugin used for registry tests.
type stubInput struct{ name string }

func (s *stubInput) Name() string  { return s.name }
func (s *stubInput) Phase() Phase  { return PhaseInput }
func (s *stubInput) Run(_ context.Context, _ *TaskContext) ([]*entry.Entry, error) {
	return nil, nil
}

func newStubFactory(name string) Factory {
	return func(_ map[string]any, _ *store.SQLiteStore) (Plugin, error) {
		return &stubInput{name: name}, nil
	}
}

func TestRegisterAndLookup(t *testing.T) {
	resetForTest()

	Register(&Descriptor{
		PluginName:  "myplugin",
		Role: RoleSource,
		Factory:     newStubFactory("myplugin"),
	})

	d, ok := Lookup("myplugin")
	if !ok {
		t.Fatal("expected to find registered plugin")
	}
	if d.PluginName != "myplugin" {
		t.Errorf("want name %q, got %q", "myplugin", d.PluginName)
	}
}

func TestLookupMiss(t *testing.T) {
	resetForTest()
	_, ok := Lookup("nonexistent")
	if ok {
		t.Error("expected miss, got hit")
	}
}

func TestRegisterPanicOnDuplicate(t *testing.T) {
	resetForTest()
	Register(&Descriptor{PluginName: "dup", Role: RoleSource, Factory: newStubFactory("dup")})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register(&Descriptor{PluginName: "dup", Role: RoleSource, Factory: newStubFactory("dup")})
}

func TestAll(t *testing.T) {
	resetForTest()
	Register(&Descriptor{PluginName: "b", Role: RoleSource, Factory: newStubFactory("b")})
	Register(&Descriptor{PluginName: "a", Role: RoleProcessor, Factory: newStubFactory("a")})

	all := All()
	if len(all) != 2 {
		t.Fatalf("want 2, got %d", len(all))
	}
	if all[0].PluginName != "a" || all[1].PluginName != "b" {
		t.Errorf("All() not sorted by name: got %v, %v", all[0].PluginName, all[1].PluginName)
	}
}

