package plugin

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/store"
)

// resetForTest clears the global registry between test cases.
func resetForTest() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]*Descriptor{}
}

type stubPlugin struct{ name string }

func (s *stubPlugin) Name() string { return s.name }

func newStubFactory(name string) Factory {
	return func(_ map[string]any, _ *store.SQLiteStore) (Plugin, error) {
		return &stubPlugin{name: name}, nil
	}
}

func TestRegisterAndLookup(t *testing.T) {
	resetForTest()

	Register(&Descriptor{
		PluginName: "myplugin",
		Role:       RoleSource,
		Factory:    newStubFactory("myplugin"),
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

// TestDescriptorCachesRoundTrip confirms Caches survives Register/Lookup
// intact. Trivial today, but cheap insurance against a future refactor that
// might copy descriptors and miss this slice.
func TestDescriptorCachesRoundTrip(t *testing.T) {
	resetForTest()
	Register(&Descriptor{
		PluginName: "with_caches",
		Role:       RoleProcessor,
		Factory:    newStubFactory("with_caches"),
		Caches: []CacheInfo{
			{Name: "cache_a", Display: "Cache A"},
			{Name: "cache_b", Display: "Cache B"},
		},
	})

	d, ok := Lookup("with_caches")
	if !ok {
		t.Fatal("plugin not found")
	}
	if len(d.Caches) != 2 {
		t.Fatalf("Caches: got %d entries, want 2", len(d.Caches))
	}
	if d.Caches[0].Name != "cache_a" || d.Caches[0].Display != "Cache A" {
		t.Errorf("Caches[0]: got %+v", d.Caches[0])
	}
	if d.Caches[1].Name != "cache_b" || d.Caches[1].Display != "Cache B" {
		t.Errorf("Caches[1]: got %+v", d.Caches[1])
	}
}
