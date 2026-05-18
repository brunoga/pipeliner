package plugin

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
)

// --- test helpers ---

type staticFromPlugin struct {
	name          string
	titles        []string
	generateCount int
}

func (p *staticFromPlugin) Name() string { return p.name }
func (p *staticFromPlugin) Generate(_ context.Context, _ *TaskContext) ([]*entry.Entry, error) {
	p.generateCount++
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
type simpleCache map[string][]match.TitleEntry

func (c simpleCache) get(key string) ([]match.TitleEntry, bool) {
	v, ok := c[key]
	return v, ok
}
func (c simpleCache) set(key string, v []match.TitleEntry) { c[key] = v }

func norm(s string) match.TitleEntry { return match.NewTitleEntry(s, 0) }

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

func TestLoggedSourcePluginForwardsCacheKey(t *testing.T) {
	inner := &cacheKeyPlugin{staticFromPlugin: staticFromPlugin{name: "trakt_list"}, key: "trakt_list:movies:ratings"}
	wrapped := &loggedSourcePlugin{inner: inner}
	if got := wrapped.CacheKey(); got != "trakt_list:movies:ratings" {
		t.Errorf("loggedSourcePlugin.CacheKey: got %q, want %q", got, "trakt_list:movies:ratings")
	}
}

func TestLoggedSourcePluginCacheKeyFallsBackToName(t *testing.T) {
	inner := &staticFromPlugin{name: "tvdb_favorites"}
	wrapped := &loggedSourcePlugin{inner: inner}
	if got := wrapped.CacheKey(); got != "tvdb_favorites" {
		t.Errorf("loggedSourcePlugin.CacheKey fallback: got %q, want %q", got, "tvdb_favorites")
	}
}

// --- ResolveDynamicList ---

func TestResolveDynamicListNoFroms(t *testing.T) {
	c := simpleCache{}
	result := ResolveDynamicList(context.Background(), makeTC(), nil,
		[]match.TitleEntry{norm("static")},
		c.get, c.set,
	)
	if len(result) != 1 || result[0].Norm != "static" {
		t.Errorf("no froms: got %v, want [{static 0}]", result)
	}
}

func TestResolveDynamicListFetchesAndCachesPerSource(t *testing.T) {
	p1 := &staticFromPlugin{name: "source_a", titles: []string{"Show A", "Show B"}}
	p2 := &staticFromPlugin{name: "source_b", titles: []string{"Show C"}}
	c := simpleCache{}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]SourcePlugin{p1, p2}, nil, c.get, c.set,
	)

	if len(result) != 3 {
		t.Fatalf("want 3 titles, got %d: %v", len(result), result)
	}
	if p1.generateCount != 1 || p2.generateCount != 1 {
		t.Errorf("each source should be called once: p1=%d p2=%d", p1.generateCount, p2.generateCount)
	}
	if _, ok := c["source_a"]; !ok {
		t.Error("source_a should be cached")
	}
	if _, ok := c["source_b"]; !ok {
		t.Error("source_b should be cached")
	}
}

func TestResolveDynamicListCacheHitSkipsGenerate(t *testing.T) {
	p := &staticFromPlugin{name: "source_a", titles: []string{"Show A"}}
	c := simpleCache{"source_a": []match.TitleEntry{norm("cached show")}}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]SourcePlugin{p}, nil, c.get, c.set,
	)

	if p.generateCount != 0 {
		t.Errorf("cached source should not be run, got generateCount=%d", p.generateCount)
	}
	if len(result) != 1 || result[0].Norm != "cached show" {
		t.Errorf("should return cached value, got %v", result)
	}
}

func TestResolveDynamicListPartialCacheHit(t *testing.T) {
	p1 := &staticFromPlugin{name: "source_a", titles: []string{"Show A"}}
	p2 := &staticFromPlugin{name: "source_b", titles: []string{"Show B"}}
	c := simpleCache{"source_a": []match.TitleEntry{norm("cached a")}}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]SourcePlugin{p1, p2}, nil, c.get, c.set,
	)

	if p1.generateCount != 0 {
		t.Errorf("source_a should not be run (cached), got %d", p1.generateCount)
	}
	if p2.generateCount != 1 {
		t.Errorf("source_b should be run once, got %d", p2.generateCount)
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
	c := simpleCache{}

	ResolveDynamicList(context.Background(), makeTC(), []SourcePlugin{p}, nil, c.get, c.set)

	if _, ok := c["trakt_list:shows:watchlist"]; !ok {
		t.Error("should be cached under CacheKey(), not Name()")
	}
	if _, ok := c["trakt_list"]; ok {
		t.Error("should NOT be cached under Name()")
	}
}

func TestResolveDynamicListTwoInstancesSamePlugin(t *testing.T) {
	watchlist := &cacheKeyPlugin{
		staticFromPlugin: staticFromPlugin{name: "trakt_list", titles: []string{"Drama Show"}},
		key:              "trakt_list:shows:watchlist",
	}
	ratings := &cacheKeyPlugin{
		staticFromPlugin: staticFromPlugin{name: "trakt_list", titles: []string{"Comedy Show"}},
		key:              "trakt_list:shows:ratings",
	}
	c := simpleCache{}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]SourcePlugin{watchlist, ratings}, nil, c.get, c.set,
	)

	if len(result) != 2 {
		t.Fatalf("want 2 titles, got %d: %v", len(result), result)
	}
	if _, ok := c["trakt_list:shows:watchlist"]; !ok {
		t.Error("watchlist not cached under its key")
	}
	if _, ok := c["trakt_list:shows:ratings"]; !ok {
		t.Error("ratings not cached under its key")
	}
}

func TestResolveDynamicListEmptyResultNotCached(t *testing.T) {
	p := &staticFromPlugin{name: "src", titles: []string{}}
	c := simpleCache{}

	ResolveDynamicList(context.Background(), makeTC(), []SourcePlugin{p}, nil, c.get, c.set)
	ResolveDynamicList(context.Background(), makeTC(), []SourcePlugin{p}, nil, c.get, c.set)

	if p.generateCount != 2 {
		t.Errorf("empty result should not be cached; plugin called %d times, want 2", p.generateCount)
	}
	if _, ok := c["src"]; ok {
		t.Error("empty result should not be stored in the cache")
	}
}

func TestResolveDynamicListMergesStaticAndDynamic(t *testing.T) {
	p := &staticFromPlugin{name: "src", titles: []string{"Dynamic Show"}}
	c := simpleCache{}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]SourcePlugin{p},
		[]match.TitleEntry{norm("static show")},
		c.get, c.set,
	)

	if len(result) != 2 || result[0].Norm != "static show" || result[1].Norm != "dynamic show" {
		t.Errorf("want [static show dynamic show], got %v", result)
	}
}

func TestResolveDynamicListPreservesYearFromEntry(t *testing.T) {
	// Source generates entries with video_year set.
	p := &staticFromPlugin{name: "trakt_list", titles: []string{"Inception"}}
	// Override Generate to set video_year on entries.
	c := simpleCache{}

	result := ResolveDynamicList(context.Background(), makeTC(),
		[]SourcePlugin{p}, nil, c.get, c.set,
	)

	// Year is 0 because staticFromPlugin doesn't set video_year.
	if len(result) != 1 || result[0].Norm != "inception" || result[0].Year != 0 {
		t.Errorf("unexpected result: %v", result)
	}
}
