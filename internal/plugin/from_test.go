package plugin

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

// --- test helpers ---

type staticFromPlugin struct {
	name    string
	titles  []string
	runCount int
}

func (p *staticFromPlugin) Name() string        { return p.name }
func (p *staticFromPlugin) Phase() Phase        { return PhaseFrom }
func (p *staticFromPlugin) Run(_ context.Context, _ *TaskContext) ([]*entry.Entry, error) {
	p.runCount++
	out := make([]*entry.Entry, len(p.titles))
	for i, t := range p.titles {
		out[i] = entry.New(t, "")
	}
	return out, nil
}

// cacheKeyPlugin adds CacheKey() to staticFromPlugin.
type cacheKeyPlugin struct {
	staticFromPlugin
	key string
}

func (p *cacheKeyPlugin) CacheKey() string { return p.key }

func makeTC() *TaskContext {
	return &TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// simpleCache is an in-memory cache for testing ResolveDynamicList.
type simpleCache map[string][]string

func (c simpleCache) get(key string) ([]string, bool) {
	v, ok := c[key]
	return v, ok
}
func (c simpleCache) set(key string, v []string) { c[key] = v }

// --- sourceKey ---

func TestSourceKeyFallsBackToName(t *testing.T) {
	p := &staticFromPlugin{name: "my_plugin"}
	if got := sourceKey(p); got != "my_plugin" {
		t.Errorf("sourceKey: got %q, want %q", got, "my_plugin")
	}
}

func TestSourceKeyUsesCacheKeyer(t *testing.T) {
	p := &cacheKeyPlugin{staticFromPlugin: staticFromPlugin{name: "my_plugin"}, key: "my_plugin:shows:watchlist"}
	if got := sourceKey(p); got != "my_plugin:shows:watchlist" {
		t.Errorf("sourceKey: got %q, want %q", got, "my_plugin:shows:watchlist")
	}
}

func TestLoggedFromPluginForwardsCacheKey(t *testing.T) {
	inner := &cacheKeyPlugin{staticFromPlugin: staticFromPlugin{name: "trakt_list"}, key: "trakt_list:movies:ratings"}
	wrapped := &loggedFromPlugin{inner: inner}
	if got := wrapped.CacheKey(); got != "trakt_list:movies:ratings" {
		t.Errorf("loggedFromPlugin.CacheKey: got %q, want %q", got, "trakt_list:movies:ratings")
	}
}

func TestLoggedFromPluginCacheKeyFallsBackToName(t *testing.T) {
	inner := &staticFromPlugin{name: "tvdb_favorites"}
	wrapped := &loggedFromPlugin{inner: inner}
	if got := wrapped.CacheKey(); got != "tvdb_favorites" {
		t.Errorf("loggedFromPlugin.CacheKey fallback: got %q, want %q", got, "tvdb_favorites")
	}
}

// --- ResolveDynamicList ---

func TestResolveDynamicListNoFroms(t *testing.T) {
	cache := simpleCache{}
	result := ResolveDynamicList(context.Background(), makeTC(), nil,
		[]string{"static"},
		cache.get, cache.set,
		strings.ToLower,
	)
	if len(result) != 1 || result[0] != "static" {
		t.Errorf("no froms: got %v, want [static]", result)
	}
}

func TestResolveDynamicListFetchesAndCachesPerSource(t *testing.T) {
	p1 := &staticFromPlugin{name: "source_a", titles: []string{"Show A", "Show B"}}
	p2 := &staticFromPlugin{name: "source_b", titles: []string{"Show C"}}
	cache := simpleCache{}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]InputPlugin{p1, p2},
		nil,
		cache.get, cache.set,
		strings.ToLower,
	)

	if len(result) != 3 {
		t.Fatalf("want 3 titles, got %d: %v", len(result), result)
	}
	if p1.runCount != 1 || p2.runCount != 1 {
		t.Errorf("each source should be called once: p1=%d p2=%d", p1.runCount, p2.runCount)
	}
	// Each source cached under its own key.
	if _, ok := cache["source_a"]; !ok {
		t.Error("source_a should be cached")
	}
	if _, ok := cache["source_b"]; !ok {
		t.Error("source_b should be cached")
	}
}

func TestResolveDynamicListCacheHitSkipsRun(t *testing.T) {
	p := &staticFromPlugin{name: "source_a", titles: []string{"Show A"}}
	cache := simpleCache{"source_a": []string{"cached show"}}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]InputPlugin{p},
		nil,
		cache.get, cache.set,
		strings.ToLower,
	)

	if p.runCount != 0 {
		t.Errorf("cached source should not be run, got runCount=%d", p.runCount)
	}
	if len(result) != 1 || result[0] != "cached show" {
		t.Errorf("should return cached value, got %v", result)
	}
}

func TestResolveDynamicListPartialCacheHit(t *testing.T) {
	// source_a is cached; source_b is not — only source_b should be fetched.
	p1 := &staticFromPlugin{name: "source_a", titles: []string{"Show A"}}
	p2 := &staticFromPlugin{name: "source_b", titles: []string{"Show B"}}
	cache := simpleCache{"source_a": []string{"cached a"}}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]InputPlugin{p1, p2},
		nil,
		cache.get, cache.set,
		strings.ToLower,
	)

	if p1.runCount != 0 {
		t.Errorf("source_a should not be run (cached), got %d", p1.runCount)
	}
	if p2.runCount != 1 {
		t.Errorf("source_b should be run once, got %d", p2.runCount)
	}
	if len(result) != 2 {
		t.Errorf("want 2 results, got %d: %v", len(result), result)
	}
}

func TestResolveDynamicListCacheKeyerUsedAsKey(t *testing.T) {
	p := &cacheKeyPlugin{
		staticFromPlugin: staticFromPlugin{name: "trakt_list", titles: []string{"Show A"}},
		key:              "trakt_list:shows:watchlist",
	}
	cache := simpleCache{}

	ResolveDynamicList(context.Background(), makeTC(),
		[]InputPlugin{p},
		nil,
		cache.get, cache.set,
		strings.ToLower,
	)

	if _, ok := cache["trakt_list:shows:watchlist"]; !ok {
		t.Error("should be cached under CacheKey(), not Name()")
	}
	if _, ok := cache["trakt_list"]; ok {
		t.Error("should NOT be cached under Name()")
	}
}

func TestResolveDynamicListTwoInstancesSamePlugin(t *testing.T) {
	// Two trakt_list instances with different lists — must be cached separately.
	watchlist := &cacheKeyPlugin{
		staticFromPlugin: staticFromPlugin{name: "trakt_list", titles: []string{"Drama Show"}},
		key:              "trakt_list:shows:watchlist",
	}
	ratings := &cacheKeyPlugin{
		staticFromPlugin: staticFromPlugin{name: "trakt_list", titles: []string{"Comedy Show"}},
		key:              "trakt_list:shows:ratings",
	}
	cache := simpleCache{}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]InputPlugin{watchlist, ratings},
		nil,
		cache.get, cache.set,
		strings.ToLower,
	)

	if len(result) != 2 {
		t.Fatalf("want 2 titles, got %d: %v", len(result), result)
	}
	if _, ok := cache["trakt_list:shows:watchlist"]; !ok {
		t.Error("watchlist not cached under its key")
	}
	if _, ok := cache["trakt_list:shows:ratings"]; !ok {
		t.Error("ratings not cached under its key")
	}
}

func TestResolveDynamicListEmptyResultNotCached(t *testing.T) {
	p := &staticFromPlugin{name: "src", titles: []string{}}
	cache := simpleCache{}

	ResolveDynamicList(context.Background(), makeTC(), []InputPlugin{p}, nil, cache.get, cache.set, strings.ToLower)
	ResolveDynamicList(context.Background(), makeTC(), []InputPlugin{p}, nil, cache.get, cache.set, strings.ToLower)

	if p.runCount != 2 {
		t.Errorf("empty result should not be cached; plugin called %d times, want 2", p.runCount)
	}
	if _, ok := cache["src"]; ok {
		t.Error("empty result should not be stored in the cache")
	}
}

func TestResolveDynamicListMergesStaticAndDynamic(t *testing.T) {
	p := &staticFromPlugin{name: "src", titles: []string{"Dynamic Show"}}
	cache := simpleCache{}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]InputPlugin{p},
		[]string{"static show"},
		cache.get, cache.set,
		strings.ToLower,
	)

	if len(result) != 2 || result[0] != "static show" || result[1] != "dynamic show" {
		t.Errorf("want [static show dynamic show], got %v", result)
	}
}
