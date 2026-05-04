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
		PluginPhase: PhaseInput,
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
	Register(&Descriptor{PluginName: "dup", PluginPhase: PhaseInput, Factory: newStubFactory("dup")})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register(&Descriptor{PluginName: "dup", PluginPhase: PhaseInput, Factory: newStubFactory("dup")})
}

func TestAll(t *testing.T) {
	resetForTest()
	Register(&Descriptor{PluginName: "b", PluginPhase: PhaseInput, Factory: newStubFactory("b")})
	Register(&Descriptor{PluginName: "a", PluginPhase: PhaseFilter, Factory: newStubFactory("a")})

	all := All()
	if len(all) != 2 {
		t.Fatalf("want 2, got %d", len(all))
	}
	if all[0].PluginName != "a" || all[1].PluginName != "b" {
		t.Errorf("All() not sorted by name: got %v, %v", all[0].PluginName, all[1].PluginName)
	}
}

func TestAllByPhase(t *testing.T) {
	resetForTest()
	Register(&Descriptor{PluginName: "in1", PluginPhase: PhaseInput, Factory: newStubFactory("in1")})
	Register(&Descriptor{PluginName: "in2", PluginPhase: PhaseInput, Factory: newStubFactory("in2")})
	Register(&Descriptor{PluginName: "f1", PluginPhase: PhaseFilter, Factory: newStubFactory("f1")})

	inputs := AllByPhase(PhaseInput)
	if len(inputs) != 2 {
		t.Fatalf("want 2 inputs, got %d", len(inputs))
	}
	// sorted by name
	if inputs[0].PluginName != "in1" || inputs[1].PluginName != "in2" {
		t.Errorf("want [in1, in2], got [%q, %q]", inputs[0].PluginName, inputs[1].PluginName)
	}

	filters := AllByPhase(PhaseFilter)
	if len(filters) != 1 || filters[0].PluginName != "f1" {
		t.Errorf("AllByPhase filter: unexpected result %v", filters)
	}
}
