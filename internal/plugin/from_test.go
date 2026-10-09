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

// --- show identity ---

// showsFromPlugin emits entries carrying a tvdb_id, the way tvdb_favorites,
// library_shows and trakt_list do.
type showsFromPlugin struct {
	name  string
	shows map[string]any // title -> tvdb_id (absent when nil)
	order []string
}

func (p *showsFromPlugin) Name() string { return p.name }
func (p *showsFromPlugin) Generate(_ context.Context, _ *TaskContext) ([]*entry.Entry, error) {
	out := make([]*entry.Entry, 0, len(p.order))
	for _, title := range p.order {
		e := entry.New(title, "")
		if id := p.shows[title]; id != nil {
			e.Set("tvdb_id", id)
		}
		out = append(out, e)
	}
	return out, nil
}

// TestResolveDynamicListCarriesTheShowID: the list is what the series/movies
// filters match a release against, and a title alone cannot separate two shows
// that normalise to the same string. The id the source published has to
// survive the trip into the TitleEntry.
func TestResolveDynamicListCarriesTheShowID(t *testing.T) {
	src := &showsFromPlugin{
		name:  "tvdb_favorites",
		order: []string{"Tomb Raider", "Tomb Raider: The Legend of Lara Croft", "Severance"},
		shows: map[string]any{
			"Tomb Raider":                           "450360",
			"Tomb Raider: The Legend of Lara Croft": 409591, // numeric shape too
			// Severance: a source that publishes no id at all.
		},
	}
	got := ResolveDynamicList(context.Background(), makeTC(),
		[]SourcePlugin{src}, nil, simpleCache{}.get, simpleCache{}.set)

	byNorm := map[string]match.TitleEntry{}
	for _, te := range got {
		byNorm[te.Norm] = te
	}
	if id := byNorm["tomb raider"].TVDBID; id != "450360" {
		t.Errorf("Tomb Raider: TVDBID = %q, want 450360", id)
	}
	if id := byNorm["tomb raider the legend of lara croft"].TVDBID; id != "409591" {
		t.Errorf("the anime: TVDBID = %q, want 409591", id)
	}
	if id := byNorm["severance"].TVDBID; id != "" {
		t.Errorf("a show with no published id must carry no id, got %q", id)
	}
}

// TestResolveDynamicListCachesTheShowID: the cache is what later runs read, so
// an id dropped on the way in would be missing for the rest of the TTL.
func TestResolveDynamicListCachesTheShowID(t *testing.T) {
	src := &showsFromPlugin{
		name:  "tvdb_favorites",
		order: []string{"Severance"},
		shows: map[string]any{"Severance": "371980"},
	}
	c := simpleCache{}
	ResolveDynamicList(context.Background(), makeTC(),
		[]SourcePlugin{src}, nil, c.get, c.set)

	cached, ok := c["tvdb_favorites"]
	if !ok {
		t.Fatalf("nothing cached under tvdb_favorites: %v", c)
	}
	if len(cached) != 1 || cached[0].TVDBID != "371980" {
		t.Errorf("cached entry = %+v, want TVDBID 371980", cached)
	}
}
