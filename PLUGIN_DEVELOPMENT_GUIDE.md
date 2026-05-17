# Plugin Development Guide

This guide covers everything you need to write a new pipeliner plugin: the
role interfaces, registration, the entry model, persistence, caching, config
validation, and source-plugin patterns.

## Table of Contents

1. [Concepts](#concepts)
2. [Plugin roles](#plugin-roles)
3. [Registering a plugin](#registering-a-plugin)
4. [The Entry data model](#the-entry-data-model)
5. [Standard field system](#standard-field-system)
6. [TaskContext](#taskcontext)
7. [Role interfaces](#role-interfaces)
   - [SourcePlugin](#sourceplugin)
   - [ProcessorPlugin](#processorplugin)
   - [SinkPlugin](#sinkplugin)
   - [ShutdownPlugin](#shutdownplugin)
   - [SearchPlugin](#searchplugin)
8. [Config validation](#config-validation)
9. [Persistence: the store and bucket API](#persistence-the-store-and-bucket-api)
10. [Caching](#caching)
11. [String interpolation](#string-interpolation)
12. [Registering in main.go](#registering-in-maingo)
13. [Complete examples](#complete-examples)

---

## Concepts

A pipeline connects **nodes** — each node is a configured plugin instance. Nodes
are wired together via `upstream=` in the config and execute in topological order.
Entries flow from source nodes through processor nodes to sink nodes.

A plugin is a Go struct that:
- implements `plugin.Plugin` (`Name() string`)
- implements one of the three role interfaces (`SourcePlugin`, `ProcessorPlugin`, or `SinkPlugin`)
- is constructed by a **factory function** in `plugin.Descriptor`
- registers itself via `plugin.Register` in an `init()` function

---

## Plugin roles

| Role | Interface | `input()` / `process()` / `output()` | Purpose |
|------|-----------|--------------------------------------|---------|
| **source** | `SourcePlugin` | `input("name", …)` | Produce entries from an external source |
| **processor** | `ProcessorPlugin` | `process("name", upstream=…, …)` | Filter, enrich, or transform entries |
| **sink** | `SinkPlugin` | `output("name", upstream=…, …)` | Act on accepted entries (download, notify, etc.) |

The executor determines a plugin's role from `Descriptor.Role`.

---

## Registering a plugin

Every plugin registers itself via `plugin.Register` inside an `init()` function.

```go
package myfilter

import (
    "context"

    "github.com/brunoga/pipeliner/internal/entry"
    "github.com/brunoga/pipeliner/internal/plugin"
    "github.com/brunoga/pipeliner/internal/store"
)

func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "my_filter",          // referenced in config: process("my_filter", …)
        Description: "one-line description shown by list-plugins",
        Role:        plugin.RoleProcessor,
        Produces:    []string{"my_field"},  // fields written on every passing entry
        MayProduce:  []string{},           // fields written only when data is available
        Requires:    nil,                  // use RequireAll / RequireAny helpers
        Factory:     newPlugin,
        Validate:    validate,             // optional but recommended
        Schema: []plugin.FieldSchema{      // optional, enables visual editor form fields
            {Key: "threshold", Type: plugin.FieldTypeInt, Default: 5,
             Hint: "Minimum value to accept"},
        },
    })
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    threshold := 5
    if v, ok := cfg["threshold"].(int); ok {
        threshold = v
    }
    return &myFilterPlugin{threshold: threshold}, nil
}

func validate(cfg map[string]any) []error {
    return plugin.OptUnknownKeys(cfg, "my_filter", "threshold")
}
```

### Descriptor fields

| Field | Type | Description |
|-------|------|-------------|
| `PluginName` | `string` | Name used in config and logs |
| `Description` | `string` | Short description for `pipeliner list-plugins` |
| `Role` | `Role` | `RoleSource`, `RoleProcessor`, or `RoleSink` |
| `Produces` | `[]string` | Fields written on **every** passing entry; used by the DAG validator for reachability checks |
| `MayProduce` | `[]string` | Fields written only on **some** entries (e.g. when a lookup succeeds or parsing matches). The validator allows these to satisfy a `Requires` group but emits a warning — helps catch pipelines that may silently no-op |
| `Requires` | `[][]string` | Field requirements as AND-of-OR groups. Use `RequireAll("a","b")` for "a AND b must be present" or `RequireAny("a","b")` for "at least one of a or b". The validator errors when no upstream produces any field in a group; warns when the only match is a `MayProduce` field |
| `Factory` | `func(map[string]any, *store.SQLiteStore) (Plugin, error)` | Constructor |
| `Validate` | `func(map[string]any) []error` | Optional config validator |
| `Schema` | `[]FieldSchema` | Optional — enables typed form fields in the visual editor |
| `IsListPlugin` | `bool` | Mark source plugins whose entry titles can feed a `list=` port on `series`, `movies`, or `discover`. The visual editor shows a teal **list** badge and allows the plugin (or a function wrapping it) to be connected to list ports |
| `IsSearchPlugin` | `bool` | Mark source plugins that implement `SearchPlugin` and can feed a `search=` port on `discover`. The visual editor shows a blue **search** badge |
| `AcceptsList` | `bool` | Declare that this plugin accepts a `list=` config key (a slice of list-plugin configs or mini-pipeline functions). Used by the visual editor to render the teal list port |
| `AcceptsSearch` | `bool` | Declare that this plugin accepts a `search=` config key. Used by the visual editor to render the search port |

---

## The Entry data model

`entry.Entry` is the core unit that flows through the pipeline.

```go
type Entry struct {
    Title        string         // human-readable name (may be updated by metainfo)
    URL          string         // canonical download URL
    OriginalURL  string         // URL as received from the source; never mutated
    State        State          // Undecided | Accepted | Rejected | Failed
    RejectReason string
    FailReason   string
    Task         string         // owning pipeline name, set by the executor
    Fields       map[string]any // arbitrary metadata bag
}
```

### State transitions

```go
e.Accept()              // Undecided → Accepted  (no-op if already Rejected)
e.Reject("reason")      // any → Rejected        (always wins over Accept)
e.Fail("reason")        // any → Failed

e.IsUndecided() bool
e.IsAccepted()  bool
e.IsRejected()  bool
e.IsFailed()    bool
```

### Field accessors

```go
e.Set("my_field", value)         // write any value
v, ok := e.Get("my_field")       // read; ok=false if absent
s := e.GetString("series_name")  // returns "" if absent or wrong type
n := e.GetInt("torrent_seeds")   // returns 0 if absent or wrong type
b := e.GetBool("enriched")       // returns false if absent
```

### Helper setters

`entry.SetVideoInfo`, `entry.SetSeriesInfo`, `entry.SetMovieInfo`,
`entry.SetTorrentInfo`, `entry.SetFileInfo`, `entry.SetRSSInfo`,
`entry.SetGenericInfo` write standard fields in bulk. Use these instead of
individual `Set` calls when writing multiple related fields.

---

## Standard field system

Fields follow a tiered naming convention with prefixes:

| Prefix | Tier | Set by |
|--------|------|--------|
| *(none)* | GenericInfo | all metainfo providers |
| `video_` | VideoInfo | metainfo_tvdb, metainfo_tmdb, metainfo_trakt, movies |
| `series_` | SeriesInfo | series, metainfo_series, metainfo_tvdb |
| `movie_` | MovieInfo | movies |
| `torrent_` | TorrentInfo | rss, jackett_search, jackett, metainfo_torrent, metainfo_magnet |
| `file_` | FileInfo | filesystem |
| `rss_` | RSSInfo | rss |

The full list of constants is in `internal/entry/info.go`.

**Key fields:**

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Canonical enriched display name (set by external providers) |
| `raw_title` | string | Original entry title from the source |
| `enriched` | bool | `true` when any external metainfo provider enriched this entry |
| `series_episode_id` | string | Episode ID (e.g. `S02E05`) — used as dedup key |
| `video_quality` | string | Parsed quality string |
| `video_resolution` | string | Resolution tag (e.g. `1080p`) |
| `torrent_seeds` | int | Seeder count |
| `file_location` | string | Absolute local file path |

---

## TaskContext

`plugin.TaskContext` is passed to every plugin call.

```go
type TaskContext struct {
    Name   string         // pipeline name
    Config map[string]any // this plugin's config block
    Logger *slog.Logger   // pipeline-scoped structured logger
    DryRun bool           // sinks must skip side effects when true
}
```

Log at the appropriate level:

```go
tc.Logger.Info("processing", "entry", e.Title)
tc.Logger.Warn("metainfo lookup failed", "entry", e.Title, "err", err)
tc.Logger.Debug("cache hit", "key", cacheKey)
```

---

## Role interfaces

### SourcePlugin

Sources produce entries from external sources (RSS, files, APIs).

```go
type SourcePlugin interface {
    Plugin
    Generate(ctx context.Context, tc *TaskContext) ([]*entry.Entry, error)
}
```

**Rules:**
- Return fresh entries from scratch; do not receive any input entries.
- Deduplicate by URL within a single call when possible.
- Errors from individual items should be logged and skipped; only fatal
  errors (network unreachable, auth failure) should be returned.
- Returned entries start in `Undecided` state.

**Example skeleton:**

```go
type mySource struct { url string }

func (p *mySource) Name() string { return "my_source" }

func (p *mySource) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
    items, err := fetchItems(ctx, p.url)
    if err != nil {
        return nil, fmt.Errorf("my_source: fetch: %w", err)
    }
    out := make([]*entry.Entry, 0, len(items))
    for _, item := range items {
        e := entry.New(item.Title, item.URL)
        e.SetGenericInfo(entry.GenericInfo{
            Description:   item.Description,
            PublishedDate: item.Date,
        })
        out = append(out, e)
    }
    return out, nil
}
```

### ProcessorPlugin

Processors transform entries. The returned slice is what passes downstream —
entries absent from the output are considered filtered out.

```go
type ProcessorPlugin interface {
    Plugin
    Process(ctx context.Context, tc *TaskContext, entries []*entry.Entry) ([]*entry.Entry, error)
}
```

**Rules:**
- Call `e.Reject(reason)` on entries you drop from the output, so the
  executor can count and report them correctly.
- Entries with `e.IsRejected() || e.IsFailed()` arriving in your input
  should normally be skipped (or passed through unchanged).
- Return the same slice when nothing was filtered (avoid allocating):
  `return entries, nil`
- For stateful processors (e.g. `seen`): read state first, apply the
  filter, then persist state within the same `Process()` call. There is no
  separate learn phase.

**Filter example (accept/reject):**

```go
type myFilter struct { minRating float64 }

func (p *myFilter) Name() string { return "my_filter" }

func (p *myFilter) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
    out := make([]*entry.Entry, 0, len(entries))
    for _, e := range entries {
        if e.IsRejected() || e.IsFailed() {
            // pass failed/rejected entries through without counting them
            out = append(out, e)
            continue
        }
        rating, _ := e.Get("video_rating")
        if r, ok := rating.(float64); ok && r >= p.minRating {
            out = append(out, e)
        } else {
            e.Reject(fmt.Sprintf("rating %.1f below minimum %.1f", r, p.minRating))
        }
    }
    return out, nil
}
```

**Enrichment example (annotate all, return all):**

```go
func (p *myMetaPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
    for _, e := range entries {
        if e.IsRejected() || e.IsFailed() {
            continue
        }
        if err := p.annotate(ctx, tc, e); err != nil {
            tc.Logger.Warn("annotation failed", "entry", e.Title, "err", err)
        }
    }
    return entries, nil
}
```

Use `entry.PassThrough(entries)` as a helper when you need to return only
the non-rejected entries from a filter:

```go
// After calling e.Reject() on unwanted entries:
return entry.PassThrough(entries), nil
```

### SinkPlugin

Sinks consume entries and perform side effects.

```go
type SinkPlugin interface {
    Plugin
    Consume(ctx context.Context, tc *TaskContext, entries []*entry.Entry) error
}
```

**Rules:**
- Call `entry.FilterAccepted(entries)` to get only accepted entries.
- Check `tc.DryRun` and skip all external side effects when it is `true`.
- Use `e.Fail("reason")` on entries that could not be processed so they
  will be retried on the next run.

```go
func (p *mySink) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
    if tc.DryRun {
        return nil
    }
    for _, e := range entry.FilterAccepted(entries) {
        if err := p.send(ctx, e); err != nil {
            tc.Logger.Error("send failed", "entry", e.Title, "err", err)
            e.Fail("my_sink: " + err.Error())
        }
    }
    return nil
}
```

### ShutdownPlugin

Implement this optional interface when your plugin holds long-lived resources
(HTTP connections, goroutines, file handles) that must be released.

```go
type ShutdownPlugin interface {
    Plugin
    Shutdown()
}
```

`Shutdown()` is called once after all runs using this plugin are complete —
at process exit for daemon mode, or after the run for one-shot mode. It is
also called when a config reload replaces this plugin with a new instance.

### SearchPlugin

Implement this when your plugin can search by query string. Used by the
`discover` processor as a search backend via the `search=` config key.

```go
type SearchPlugin interface {
    Plugin
    Search(ctx context.Context, tc *TaskContext, query string) ([]*entry.Entry, error)
}
```

A plugin implementing `SearchPlugin` should typically also implement
`SourcePlugin.Generate()` (calling `Search` with an empty query) so it can
be used as a standalone source node.

### Mini-pipelines as list and search sources

Instead of a single plugin name, a `list=` or `search=` slot can receive a
**mini-pipeline** — a Starlark helper function that builds a small chain of
`input()`/`process()` nodes and returns the terminal node handle:

```python
# pipeliner:param type=string Trakt client ID
def unwatched_movies(client_id):
    src = input("trakt_list", client_id=client_id, type="movies", list="watchlist")
    flt = process("trakt", upstream=src, client_id=client_id, type="movies",
                  list="history", reject_matched=True, reject_unmatched=False)
    return flt

movies_node = process("movies", upstream=rss, list=[unwatched_movies(client_id=env("TRAKT_ID"))])
```

**How it works at runtime:**

1. `toGoValue()` stores a `nodeHandleRef` sentinel when a `nodeHandle` appears in a kwarg value.
2. Before the main graph is assembled, `resolveNodePipelines()` scans all pending node configs for `nodeHandleRef` values in `list=` / `search=` keys, extracts the subgraph (the returned node and all its transitive upstreams) in topological order, builds a `*plugin.NodePipeline`, and removes those nodes from the main DAG.
3. `MakeListPlugin` and `resolveSearchPlugin` detect `*NodePipeline` and instantiate a `miniPipelineSource` (or `miniPipelineSearch`) executor that runs the chain inline.
4. `CommitPlugin` is **never called** on mini-pipeline processors — sub-pipelines are title sources only and must not persist state.

**Validation rules:**

- The first node in a `search=` mini-pipeline must have `IsSearchPlugin: true`.
- All subsequent nodes must have `RoleProcessor`.
- For `list=` mini-pipelines the first node must have `RoleSource`; subsequent nodes must be processors.

---

## Config validation

Implement `Validate` in the `Descriptor` for early error reporting via
`pipeliner check`. The validate function receives the raw config map and
returns a slice of errors (never return nil for the slice itself; return
`nil` if there are no errors).

Helper functions in `internal/plugin/validate.go`:

```go
plugin.RequireString(cfg, "url", "my_plugin")       // error if absent or empty
plugin.RequireOneOf(cfg, "my_plugin", "a", "b")     // error if none are set
plugin.OptDuration(cfg, "timeout", "my_plugin")      // error if set but invalid duration
plugin.OptEnum(cfg, "mode", "my_plugin", "a", "b")  // error if set but not one of the values
plugin.OptUnknownKeys(cfg, "my_plugin", "url", "timeout") // error for unrecognized keys
```

Full validation example:

```go
func validate(cfg map[string]any) []error {
    var errs []error
    if err := plugin.RequireString(cfg, "url", "my_source"); err != nil {
        errs = append(errs, err)
    }
    if err := plugin.OptDuration(cfg, "timeout", "my_source"); err != nil {
        errs = append(errs, err)
    }
    errs = append(errs, plugin.OptUnknownKeys(cfg, "my_source", "url", "timeout")...)
    return errs
}
```

---

## Persistence: the store and bucket API

The shared SQLite store (`internal/store`) provides a simple key-value bucket
API. Each plugin gets its own bucket, namespaced automatically by plugin name
and pipeline name.

```go
func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    return &myPlugin{
        bucket: db.Bucket("my_plugin"),
    }, nil
}
```

```go
type Bucket interface {
    Put(key string, value any) error         // JSON-encodes and stores
    Get(key string, dest any) (bool, error)  // JSON-decodes; ok=false if missing
    Delete(key string) error
    Keys() ([]string, error)
    All() (map[string][]byte, error)
}
```

Use the bucket to persist state across runs:

```go
type record struct {
    Downloaded time.Time `json:"downloaded"`
    Quality    string    `json:"quality"`
}

// In Process():
var rec record
found, err := p.bucket.Get(key, &rec)
if err != nil {
    return nil, fmt.Errorf("my_plugin: read state: %w", err)
}
if found {
    // entry already seen — check for upgrade
}
// After deciding to accept:
if err := p.bucket.Put(key, record{Downloaded: time.Now(), Quality: quality}); err != nil {
    tc.Logger.Warn("failed to persist state", "key", key, "err", err)
}
```

**Bucket scope:** Calling `db.Bucket("my_plugin")` creates a bucket shared
across all pipeline runs. To scope state per-pipeline, use
`db.Bucket("my_plugin:" + tc.Name)`.

---

## Caching

For plugins that make external API calls, use `internal/cache` to avoid
repeated requests within the same run or across recent runs.

```go
import "github.com/brunoga/pipeliner/internal/cache"

type myPlugin struct {
    cache *cache.Cache[[]Item]
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    ttl := 24 * time.Hour
    if v, _ := cfg["cache_ttl"].(string); v != "" {
        d, err := time.ParseDuration(v)
        if err != nil {
            return nil, fmt.Errorf("invalid cache_ttl: %w", err)
        }
        ttl = d
    }
    return &myPlugin{
        cache: cache.NewPersistent[[]Item](ttl, db.Bucket("cache_my_plugin")),
    }, nil
}

// In Generate() or Process():
cacheKey := "my-key"
if items, ok := p.cache.Get(cacheKey); ok {
    // cache hit
    return buildEntries(items), nil
}
items, err := fetchFromAPI(ctx)
if err != nil {
    return nil, err
}
if len(items) > 0 {
    p.cache.Set(cacheKey, items) // only cache non-empty results
}
return buildEntries(items), nil
```

`cache.NewPersistent` persists the cache in the SQLite store across process
restarts. `cache.New` keeps the cache in-memory only (lost on restart).

---

## String interpolation

`internal/interp` provides `{field}` and `{{.field}}` interpolation for use
in path templates and command strings.

```go
import "github.com/brunoga/pipeliner/internal/interp"

ip, err := interp.Compile("/media/tv/{title}/Season {series_season:02d}")
if err != nil {
    return nil, fmt.Errorf("invalid path pattern: %w", err)
}

// In Process() or Consume():
result, err := ip.Render(interp.EntryData(e))
if err != nil {
    tc.Logger.Warn("render failed", "err", err)
    continue
}
e.Set("download_path", result)
```

`interp.EntryData(e)` builds a `map[string]any` with both the entry's Fields
and convenience keys (`Title`, `URL`, `Task`, `url_basename`, `timestamp`).

---

## Registering in main.go

Add a blank import of your plugin package to `cmd/pipeliner/main.go`:

```go
// Source plugins
_ "github.com/brunoga/pipeliner/plugins/source/my_source"

// Processor plugins
_ "github.com/brunoga/pipeliner/plugins/processor/filter/my_filter"
_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/my_metainfo"

// Sink plugins
_ "github.com/brunoga/pipeliner/plugins/sink/my_sink"
```

The blank import causes the `init()` function to run, which calls
`plugin.Register`. The order of imports does not matter.

---

## Complete examples

### Source plugin

```go
// plugins/source/mysite/mysite.go
package mysite

import (
    "context"
    "fmt"
    "net/http"

    "github.com/brunoga/pipeliner/internal/entry"
    "github.com/brunoga/pipeliner/internal/plugin"
    "github.com/brunoga/pipeliner/internal/store"
)

func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "mysite",
        Description: "fetch entries from mysite.example.com",
        Role:        plugin.RoleSource,
        Produces:    []string{entry.FieldDescription, entry.FieldPublishedDate},
        Factory:     newPlugin,
        Validate:    validate,
        Schema: []plugin.FieldSchema{
            {Key: "url", Type: plugin.FieldTypeString, Required: true, Hint: "Feed URL"},
        },
    })
}

type mysitePlugin struct{ url string }

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
    url, _ := cfg["url"].(string)
    if url == "" {
        return nil, fmt.Errorf("mysite: url is required")
    }
    return &mysitePlugin{url: url}, nil
}

func validate(cfg map[string]any) []error {
    var errs []error
    if err := plugin.RequireString(cfg, "url", "mysite"); err != nil {
        errs = append(errs, err)
    }
    errs = append(errs, plugin.OptUnknownKeys(cfg, "mysite", "url")...)
    return errs
}

func (p *mysitePlugin) Name() string { return "mysite" }

func (p *mysitePlugin) Generate(ctx context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
    if err != nil {
        return nil, fmt.Errorf("mysite: build request: %w", err)
    }
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("mysite: fetch: %w", err)
    }
    defer resp.Body.Close()
    // parse resp.Body, build entries...
    var out []*entry.Entry
    // out = append(out, entry.New(title, url))
    return out, nil
}
```

### Processor plugin (filter with persistence)

```go
// plugins/processor/filter/myfilter/myfilter.go
package myfilter

import (
    "context"
    "fmt"

    "github.com/brunoga/pipeliner/internal/entry"
    "github.com/brunoga/pipeliner/internal/plugin"
    "github.com/brunoga/pipeliner/internal/store"
)

func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "myfilter",
        Description: "accept entries whose score exceeds a threshold",
        Role:        plugin.RoleProcessor,
        Requires:    plugin.RequireAll("my_score"),
        Factory:     newPlugin,
        Validate:    validate,
    })
}

type myFilter struct {
    threshold float64
    bucket    store.Bucket
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    threshold := 5.0
    if v, ok := cfg["threshold"].(float64); ok {
        threshold = v
    }
    return &myFilter{
        threshold: threshold,
        bucket:    db.Bucket("myfilter"),
    }, nil
}

func validate(cfg map[string]any) []error {
    return plugin.OptUnknownKeys(cfg, "myfilter", "threshold")
}

func (p *myFilter) Name() string { return "myfilter" }

func (p *myFilter) Process(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
    out := make([]*entry.Entry, 0, len(entries))
    for _, e := range entries {
        if e.IsRejected() || e.IsFailed() {
            out = append(out, e)
            continue
        }
        score, _ := e.Get("my_score")
        s, _ := score.(float64)
        if s >= p.threshold {
            e.Accept()
            out = append(out, e)
        } else {
            e.Reject(fmt.Sprintf("myfilter: score %.1f below threshold %.1f", s, p.threshold))
        }
    }
    return out, nil
}
```

### Sink plugin

```go
// plugins/sink/mynotify/mynotify.go
package mynotify

import (
    "context"
    "fmt"
    "net/http"
    "strings"

    "github.com/brunoga/pipeliner/internal/entry"
    "github.com/brunoga/pipeliner/internal/plugin"
    "github.com/brunoga/pipeliner/internal/store"
)

func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "mynotify",
        Description: "POST accepted entries to a webhook URL",
        Role:        plugin.RoleSink,
        Factory:     newPlugin,
        Validate:    validate,
        Schema: []plugin.FieldSchema{
            {Key: "url", Type: plugin.FieldTypeString, Required: true, Hint: "Webhook URL"},
        },
    })
}

type myNotify struct{ url string }

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
    url, _ := cfg["url"].(string)
    if url == "" {
        return nil, fmt.Errorf("mynotify: url is required")
    }
    return &myNotify{url: url}, nil
}

func validate(cfg map[string]any) []error {
    var errs []error
    if err := plugin.RequireString(cfg, "url", "mynotify"); err != nil {
        errs = append(errs, err)
    }
    errs = append(errs, plugin.OptUnknownKeys(cfg, "mynotify", "url")...)
    return errs
}

func (p *myNotify) Name() string { return "mynotify" }

func (p *myNotify) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
    if tc.DryRun {
        return nil
    }
    for _, e := range entry.FilterAccepted(entries) {
        body := fmt.Sprintf(`{"title":%q,"url":%q}`, e.Title, e.URL)
        req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, strings.NewReader(body))
        if err != nil {
            tc.Logger.Error("build request failed", "err", err)
            continue
        }
        req.Header.Set("Content-Type", "application/json")
        resp, err := http.DefaultClient.Do(req)
        if err != nil {
            tc.Logger.Error("webhook failed", "entry", e.Title, "err", err)
            e.Fail("mynotify: " + err.Error())
            continue
        }
        resp.Body.Close()
        if resp.StatusCode >= 400 {
            tc.Logger.Error("webhook error", "entry", e.Title, "status", resp.StatusCode)
            e.Fail(fmt.Sprintf("mynotify: HTTP %d", resp.StatusCode))
        }
    }
    return nil
}
```

---

*See `plugins/processor/filter/regexp/` for a well-documented real-world processor,
`plugins/source/rss/` for a real-world source, and `plugins/sink/transmission/`
for a real-world sink.*
