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
   - [CommitPlugin](#commitplugin)
   - [SearchPlugin](#searchplugin)
8. [Config validation](#config-validation)
9. [Schema field types](#schema-field-types)
10. [Persistence: the store and bucket API](#persistence-the-store-and-bucket-api)
11. [Caching](#caching)
12. [Expression language](#expression-language)
13. [String interpolation](#string-interpolation)
14. [Starlark configuration built-ins](#starlark-configuration-built-ins)
15. [List and search plugin patterns](#list-and-search-plugin-patterns)
16. [Advanced utilities](#advanced-utilities)
17. [Internal plugins](#internal-plugins)
18. [Registering in main.go](#registering-in-maingo)
19. [Testing plugins](#testing-plugins)
20. [Complete examples](#complete-examples)

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

### Execution model

The executor runs nodes in **topological layer order**; nodes within a layer run serially.

- **Merge (N→1):** when a processor has multiple upstreams, the executor merges and
  deduplicates all incoming entries by URL (first-seen wins) before calling `Process`.
- **Fan-out (1→N):** when a node's output feeds multiple downstream nodes, the first
  consumer receives the original entry slice; every subsequent consumer receives a
  deep clone so that mutations in one branch do not affect other branches.
- **Sink chaining:** a sink may have downstream sink nodes. After a sink's `Consume`
  runs, the executor calls `entry.FilterAccepted(upstream)` and passes the result to
  the next chained sink — so entries failed by the first sink are not forwarded.
- **Commit phase:** after all nodes have run, the executor calls `CommitPlugin.Commit`
  for every processor that implements it, passing only the entries that were not failed
  by any sink. See [CommitPlugin](#commitplugin) for details.

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

### Error string conventions

All errors returned from plugin factories, validators, and role methods must follow
the project convention: **lowercase, no trailing punctuation**, prefixed with the
plugin name:

```go
fmt.Errorf("my_plugin: url is required")
fmt.Errorf("my_plugin: invalid timeout %q: %w", v, err)
fmt.Errorf("my_plugin: store lookup failed: %w", err)
```

This applies everywhere — factory errors, `Validate` errors, `Process`/`Consume`
return values, and rejection reasons passed to `e.Reject()`.

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
| `Requires` | `[][]string` | Field requirements as AND-of-OR groups. Use `RequireAll("a","b")` for "a AND b must be present" or `RequireAny("a","b")` for "at least one of a or b". The validator distinguishes *certain* fields (in `Produces` of every upstream path — guaranteed on every entry) from *reachable* fields (in `Produces` or `MayProduce` of any path — potentially present). A `Requires` group satisfied only by reachable fields emits a warning; a group with no reachable field at all is an error |
| `Factory` | `func(map[string]any, *store.SQLiteStore) (Plugin, error)` | Constructor |
| `Validate` | `func(map[string]any) []error` | Optional config validator |
| `Schema` | `[]FieldSchema` | Optional — enables typed form fields in the visual editor |
| `IsListPlugin` | `bool` | Mark source plugins whose entry titles can feed a `list=` port on `series`, `movies`, or `discover`. The visual editor shows a teal **list** badge and allows the plugin (or a function wrapping it) to be connected to list ports |
| `IsSearchPlugin` | `bool` | Mark source plugins that implement `SearchPlugin` and can feed a `search=` port on `discover`. The visual editor shows a blue **search** badge |
| `AcceptsList` | `bool` | Declare that this plugin accepts a `list=` config key (a slice of list-plugin configs or mini-pipeline functions). Used by the visual editor to render the teal list port |
| `AcceptsSearch` | `bool` | Declare that this plugin accepts a `search=` config key. Used by the visual editor to render the search port |
| `Internal` | `bool` | Mark plugins that are implementation details of a built-in (e.g. `route_selector` created by the `route()` Starlark builtin). Internal plugins are registered so the executor can instantiate them but are hidden from the visual editor palette and cannot be used directly in config |

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

The four state constants are also exported for direct comparison:

```go
entry.Undecided  // State = 0
entry.Accepted   // State = 1
entry.Rejected   // State = 2
entry.Failed     // State = 3
```

### Consumed entries

`e.Consume()` is used by sinks that have already handled an entry by other means
and want to prevent downstream sinks from acting on it again — without marking it
as failed (which would prevent `CommitPlugin.Commit` from running):

```go
e.Consume()        // silences subsequent sinks; entry remains Accepted for CommitPlugin
e.IsConsumed() bool
```

`entry.FilterAccepted(entries)` excludes both `Failed` entries and `Consumed` entries,
so a sink calling `e.Consume()` stops the entry from reaching any chained sinks.

### Cloning

```go
clone := e.Clone() // deep copy; mutating the clone does not affect the original
```

### Field accessors

```go
e.Set("my_field", value)         // write any value
v, ok := e.Get("my_field")       // read; ok=false if absent
s := e.GetString("series_name")  // returns "" if absent or wrong type
n := e.GetInt("torrent_seeds")   // returns 0 if absent or wrong type
b := e.GetBool("enriched")       // returns false if absent
t := e.GetTime("series_first_air_date") // returns time.Time{} if absent or wrong type
```

### Helper setters

`entry.SetVideoInfo`, `entry.SetSeriesInfo`, `entry.SetMovieInfo`,
`entry.SetTorrentInfo`, `entry.SetFileInfo`, `entry.SetRSSInfo`,
`entry.SetGenericInfo` write standard fields in bulk. Use these instead of
individual `Set` calls when writing multiple related fields.

Only non-zero fields in the struct are written — zero values are silently
skipped, so you can call multiple setters on the same entry without one
overwriting fields set by another.

```go
// Typical source plugin pattern — set whatever your data provides:
e.SetGenericInfo(entry.GenericInfo{Title: item.Title, Description: item.Desc})
e.SetRSSInfo(entry.RSSInfo{Feed: p.url, GUID: item.GUID, Link: item.Link})
if seeds > 0 {
    e.SetTorrentInfo(entry.TorrentInfo{Seeds: seeds})
}
```

**Info struct fields reference** (see `internal/entry/info.go` for the full list):

| Type | Key fields |
|------|-----------|
| `GenericInfo` | `Title`, `Description`, `PublishedDate`, `Enriched` |
| `VideoInfo` | embeds `GenericInfo` + `Year`, `Language`, `OriginalTitle`, `Country`, `Genres`, `Rating`, `Poster`, `Cast`, `ContentRating`, `Runtime`, `Trailers`, `Aliases`, `ImdbID`, `Quality`, `Resolution`, `Source`, `Is3D`, `Popularity`, `Votes` |
| `SeriesInfo` | embeds `VideoInfo` + `Season`, `Episode`, `EpisodeID`, `Network`, `Status`, `FirstAirDate`, `LastAirDate`, `NextAirDate`, `EpisodeTitle`, `EpisodeDescription`, `EpisodeAirDate`, `EpisodeImage`, `Service`, `Proper`, `Repack`, `DoubleEpisode` |
| `MovieInfo` | embeds `VideoInfo` + `Title` (movie_title), `Tagline` |
| `TorrentInfo` | embeds `GenericInfo` + `FileSize`, `FileCount`, `Files`, `Seeds`, `Leechers`, `InfoHash`, `Announce`, `AnnounceList`, `CreatedBy`, `CreationDate`, `Private` |
| `FileInfo` | embeds `GenericInfo` + `Filename`, `Extension`, `Location`, `FileSize`, `ModifiedTime` |
| `RSSInfo` | embeds `GenericInfo` + `Feed`, `GUID`, `Link`, `EnclosureURL`, `EnclosureType` |

### Package-level helpers

```go
entry.New(title, url string) *Entry           // create an Undecided entry
entry.FilterAccepted(entries) []*Entry        // Accepted and not Consumed
entry.PassThrough(entries) []*Entry           // not Rejected and not Failed
entry.ReleaseYear(e *Entry) int               // reads video_year; returns 0 if absent
```

---

## Standard field system

Fields follow a tiered naming convention with prefixes:

| Prefix | Tier | Set by |
|--------|------|--------|
| *(none)* | GenericInfo | all source and metainfo providers |
| `video_` | VideoInfo | metainfo_tvdb, metainfo_tmdb, metainfo_trakt, metainfo_file |
| `series_` | SeriesInfo | metainfo_file, metainfo_tvdb |
| `movie_` | MovieInfo | metainfo_tmdb, metainfo_file |
| `torrent_` | TorrentInfo | rss, jackett, metainfo_torrent, metainfo_magnet |
| `file_` | FileInfo | filesystem |
| `rss_` | RSSInfo | rss |

The full list of constants is in `internal/entry/info.go`.

**Key fields:**

| Field | Type | Description |
|-------|------|-------------|
| `source` | string | Origin of the entry in the form `plugin:identifier` — set by **every source plugin** (e.g. `jackett:1337x`, `rss:nyaa.si`, `filesystem:/downloads`). Never mutated by processors or sinks. |
| `title` | string | Canonical enriched display name (set by external providers) |
| `raw_title` | string | Original entry title from the source |
| `enriched` | bool | `true` when any external metainfo provider enriched this entry |
| `series_episode_id` | string | Episode ID (e.g. `S02E05`) — used as dedup key |
| `video_quality` | string | Parsed quality string |
| `video_resolution` | string | Resolution tag (e.g. `1080p`) |
| `torrent_seeds` | int | Seeder count |
| `file_location` | string | Absolute local file path |
| `_route_port` | string | Set by `route()` on matched entries; identifies which named port was matched (`entry.FieldRoutePort`) |

All field name strings have corresponding `entry.FieldXxx` constants in
`internal/entry/info.go`. Always prefer the constants over raw strings so that
renames are caught at compile time.

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
- Deduplicate by URL within a single call when possible (use a `map[string]bool`).
- Errors from individual items should be logged and skipped; only fatal
  errors (network unreachable, auth failure) should be returned.
- Returned entries start in `Undecided` state.
- Check `ctx.Err()` at the top of any retry loop or multi-item iteration; return
  immediately when the context is cancelled.

**HTTP best practices:**

Use a per-plugin `*http.Client` with a timeout, pass context to every request,
set a descriptive `User-Agent`, and retry transient 5xx errors with backoff:

```go
type mySource struct {
    url    string
    client *http.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
    return &mySource{
        url:    cfg["url"].(string),
        client: &http.Client{Timeout: 30 * time.Second},
    }, nil
}

func (p *mySource) fetch(ctx context.Context) (io.ReadCloser, error) {
    var lastErr error
    for attempt := range 3 {
        if attempt > 0 {
            select {
            case <-ctx.Done():
                return nil, ctx.Err()
            case <-time.After(time.Duration(attempt) * 2 * time.Second):
            }
        }
        req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
        if err != nil {
            return nil, fmt.Errorf("my_source: build request: %w", err)
        }
        req.Header.Set("User-Agent", "pipeliner/1.0")
        resp, err := p.client.Do(req)
        if err != nil {
            lastErr = err
            continue
        }
        if resp.StatusCode >= 500 {
            resp.Body.Close()
            lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
            continue
        }
        if resp.StatusCode != http.StatusOK {
            resp.Body.Close()
            return nil, fmt.Errorf("my_source: HTTP %d", resp.StatusCode)
        }
        return resp.Body, nil
    }
    return nil, fmt.Errorf("my_source: fetch after retries: %w", lastErr)
}
```

**URL deduplication within Generate:**

```go
seen := map[string]bool{}
for _, item := range items {
    if seen[item.URL] {
        continue
    }
    seen[item.URL] = true
    out = append(out, entry.New(item.Title, item.URL))
}
```

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
        e.Set(entry.FieldSource, "my_source:"+p.identifier)
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
  filter, then use `CommitPlugin` to persist state only after sinks confirm.
- **Compile once, evaluate many times.** Regexps, expression trees, and
  interpolation patterns are expensive to compile. Always compile them in the
  factory (inside `newPlugin`) and store the compiled form in the plugin struct.
  Call the compiled form per entry in `Process`.

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

**Per-entry error handling in filters:** When per-entry logic can fail (e.g. a
lookup or parse), log the error at `Warn` level and continue processing the rest
of the batch. Only return a non-nil error from `Process` for truly fatal
conditions (e.g. the store is corrupted). Individual entry errors should never
abort the whole batch.

```go
for _, e := range entries {
    if e.IsRejected() || e.IsFailed() {
        continue
    }
    if err := p.check(ctx, tc, e); err != nil {
        tc.Logger.Warn("check error", "entry", e.Title, "err", err)
        // do not return err — continue with remaining entries
    }
}
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
- Call `entry.FilterAccepted(entries)` to get only accepted, non-consumed entries.
- Check `tc.DryRun` and skip all external side effects when it is `true`.
- Use `e.Fail("reason")` on entries that could not be processed so they
  will be retried on the next run.
- Use `e.Consume()` when this sink has already applied the effect by other
  means and you want to silence subsequent chained sinks without failing the
  entry (e.g. a deduplication sink that found the entry already queued).

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

**Thread safety:** a single plugin instance is shared across all calls within a
pipeline run. If your sink maintains mutable state (e.g. an HTTP session ID,
a connection pool, a counter), protect it with a mutex:

```go
type mySink struct {
    mu        sync.Mutex
    sessionID string
}

func (p *mySink) sessionIDSafe() string {
    p.mu.Lock()
    defer p.mu.Unlock()
    return p.sessionID
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

**Config reload and plugin lifecycle:** In daemon mode the web UI supports hot
config reload. Reload is queued until all running tasks are idle, then:

1. Old plugin instances that implement `ShutdownPlugin` have `Shutdown()` called.
2. New plugin instances are constructed from the reloaded config.
3. In-memory state on the old instances is discarded.

Implications: do not rely on in-memory state surviving a reload. Cross-run
state must live in a persistent `Bucket`. `CommitPlugin.Commit` is never called
during shutdown — only during normal pipeline completion.

### CommitPlugin

Implement this optional interface when your processor must persist state only
**after** all downstream sinks have confirmed success. The executor calls
`Commit` once after every sink has run, passing only the entries that were
accepted by this processor and not subsequently **failed** by any sink
(across all fan-out branches, matched by URL). Entries marked `Consumed`
by a sink are still passed to `Commit` — `Consume` silences subsequent
sinks but does not prevent state from being committed.

```go
type CommitPlugin interface {
    Plugin
    Commit(ctx context.Context, tc *TaskContext, entries []*entry.Entry) error
}
```

**When to use:** Use `CommitPlugin` instead of persisting state inside `Process`
whenever you need to guarantee that state is only written when the full pipeline
(including download/output) succeeded. If a sink fails an entry, `Commit` will
not receive that entry and the state is not persisted, so the entry will be
retried on the next run.

The built-in `seen`, `series`, `movies`, and `premiere` plugins all implement
`CommitPlugin` for exactly this reason.

**Recommended pattern — separate filter and persist methods:**

Keeping the filter logic and persistence logic in distinct private methods makes
the `Process` / `Commit` pair easy to read and test independently:

```go
type myStateful struct {
    bucket store.Bucket
}

// filter checks one entry and mutates its state if needed.
func (p *myStateful) filter(tc *plugin.TaskContext, e *entry.Entry) {
    fp := fingerprint(e)
    var found bool
    if err := p.bucket.Get(fp, &found); err == nil && found {
        e.Reject("already processed")
    }
}

// persist records accepted entries as seen.
func (p *myStateful) persist(tc *plugin.TaskContext, entries []*entry.Entry) error {
    for _, e := range entries {
        fp := fingerprint(e)
        if err := p.bucket.Put(fp, true); err != nil {
            tc.Logger.Warn("persist failed", "entry", e.Title, "err", err)
        }
    }
    return nil
}

func (p *myStateful) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
    for _, e := range entries {
        if e.IsRejected() || e.IsFailed() {
            continue
        }
        p.filter(tc, e)
    }
    return entry.PassThrough(entries), nil
}

// Commit is called only for entries not failed by any downstream sink.
func (p *myStateful) Commit(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
    return p.persist(tc, entries)
}
```

> **Note:** `CommitPlugin` is intentionally **not** called for mini-pipeline
> processors used as `list=` or `search=` sources — those sub-pipelines are
> title-source only and must not persist state.

Use `CommitPlugin` when "I only want to record this if the download succeeded."
The built-in `seen`, `series`, `movies`, and `premiere` plugins all follow this
pattern.

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
def watchlist_titles(client_id):
    src = input("trakt_list", client_id=client_id, type="movies", list="watchlist")
    flt = process("regexp", upstream=src, reject=["(?i)\\bxxx\\b"])
    return flt

movies_node = process("movies", upstream=rss, list=[watchlist_titles(client_id=env("TRAKT_ID"))])
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

### Conditional branching with route()

The `route()` Starlark builtin routes entries to named ports based on ordered boolean conditions. It is not a plugin — it is a built-in function that creates a `route` processor node and N `route_selector` nodes internally:

```python
meta   = process("metainfo_file", upstream=upstream)  # sets media_type, series_*, movie_*, video_*
routes = route(meta,
    series = "media_type == 'series'",
    movies = "media_type == 'movie'")
series_path = process("metainfo_tvdb", upstream=routes.series, api_key=env("TVDB_KEY"))
movies_path = process("metainfo_tmdb", upstream=routes.movies, api_key=env("TMDB_KEY"))
output("transmission", upstream=merge(series_path, movies_path))
```

Key properties:
- Exactly one port matches each entry (first match wins); unmatched entries are rejected with `WARN`.
- The DAG validator **automatically infers field contracts** from each port's accept expression: presence-check operators (`!= ""`) promote the field to certain on that branch; absence-check operators (`== ""`) remove the field from the reachable set. No explicit annotations are needed.
- At a `merge()` of route ports the DAG validator uses **intersection** semantics for `certain` fields — fields certain on every branch remain certain; fields present only on some branches become reachable-only.
- The `_route_port` field is stamped on matched entries and is available downstream.
- See [`plugins/processor/filter/route/README.md`](plugins/processor/filter/route/README.md) for the full reference.

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

### Flexible string-or-list config keys

Config values may arrive as a bare string or as a list depending on how the user
wrote the config. A common helper for accepting either form:

```go
func toStringSlice(v any) []string {
    switch t := v.(type) {
    case string:
        if t != "" {
            return []string{t}
        }
    case []string:
        return t
    case []any:
        out := make([]string, 0, len(t))
        for _, item := range t {
            if s, ok := item.(string); ok {
                out = append(out, s)
            }
        }
        return out
    }
    return nil
}
```

This lets users write either `static: "Breaking Bad"` or `static: ["Breaking Bad", "The Office"]`.

---

## Schema field types

`Descriptor.Schema` is optional but enables typed form fields in the visual
pipeline editor. Each `FieldSchema` entry describes one config key:

```go
type FieldSchema struct {
    Key       string    // config map key, e.g. "url"
    Type      FieldType // expected value type (see table below)
    Required  bool      // whether the key must be present
    Default   any       // optional default shown as placeholder
    Enum      []string  // valid values when Type == FieldTypeEnum
    Hint      string    // one-line description shown in the editor
    Multiline bool      // open a full text-editor modal instead of inline input
}
```

Available `FieldType` values:

| Constant | Value | Go type | Notes |
|----------|-------|---------|-------|
| `FieldTypeString` | `"string"` | `string` | Plain text |
| `FieldTypePattern` | `"pattern"` | `string` | String with `{field}` runtime interpolation |
| `FieldTypeInt` | `"int"` | `int` | Integer |
| `FieldTypeBool` | `"bool"` | `bool` | Boolean |
| `FieldTypeDuration` | `"duration"` | `string` | Go duration, e.g. `"1h"`, `"30m"` |
| `FieldTypeEnum` | `"enum"` | `string` | One of the values listed in `Enum` |
| `FieldTypeList` | `"list"` | `[]string` | Ordered list of strings |
| `FieldTypeDict` | `"dict"` | `map[string]any` | Sub-plugin lists or nested config |
| `FieldTypeRuleList` | `"rule_list"` | `[]map[string]any` | Ordered `[{name, accept}]` pairs — used by `route` |

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

**Bucket naming conventions** observed across built-in plugins:

| Pattern | Example | Use |
|---------|---------|-----|
| `"plugin_name"` | `"seen"` | Global state shared across all pipelines |
| `"plugin_name:" + tc.Name` | `"premiere:new-shows"` | Per-pipeline isolated state |
| `"cache_plugin_name"` | `"cache_metainfo_tmdb"` | Cache bucket (fed to `cache.NewPersistent`) |
| `"cache_plugin_name_suffix"` | `"cache_metainfo_tvdb_ext"` | Multiple caches in one plugin |

### SeenStore: fingerprint-based deduplication

`store.SeenStore` wraps a `Bucket` with a higher-level fingerprint-based
dedup API. Use it when you need to track whether an entry (identified by an
opaque fingerprint you compute) has already been processed:

```go
import "github.com/brunoga/pipeliner/internal/store"

type myPlugin struct {
    db *store.SQLiteStore
}

// In filter():
ss := store.NewSeenStore(p.db.Bucket("my_plugin"))
fp := fmt.Sprintf("%s:%s", e.URL, e.GetString(entry.FieldSeriesEpisodeID))
if ss.IsSeen(fp) {
    e.Reject("already seen")
    return
}

// In persist() / Commit():
_ = ss.Mark(fp, store.SeenRecord{
    Title:  e.Title,
    URL:    e.URL,
    Task:   tc.Name,
    Fields: []string{"url", "series_episode_id"},
})
```

`SeenStore.IsSeen(fp string) bool` and `SeenStore.Mark(fp string, rec SeenRecord) error`
are the only methods. `SeenRecord` carries human-readable metadata (title, URL, task,
which fields formed the fingerprint, and a timestamp) that is useful for debugging via
the store inspect command.

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
    p := &myPlugin{
        cache: cache.NewPersistent[[]Item](ttl, db.Bucket("cache_my_plugin")),
    }
    p.cache.Preload() // bulk-load non-expired entries from SQLite into memory at startup
    return p, nil
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
restarts. Calling `Preload()` immediately after construction bulk-loads all
non-expired entries from the bucket into the in-memory map, eliminating
per-key SQLite reads during normal operation. If you only need in-memory
caching (no cross-run persistence), store data directly in plugin struct
fields (maps, slices) rather than using the cache package.

All `Cache[V]` methods (`Get`, `Set`, `Preload`) are nil-safe — a nil
`*Cache[V]` behaves as a disabled cache (every `Get` misses, every `Set` is
a no-op). This lets you make caching optional by setting the cache field to
nil when the user configures a zero TTL, without guarding every call site.

---

## Expression language

`internal/expr` provides a boolean infix expression evaluator used by the
`condition` filter and the `route()` builtin. Plugin authors can use it
directly whenever they need to evaluate user-supplied conditions against entry
fields.

```go
import "github.com/brunoga/pipeliner/internal/expr"

// Compile once (e.g. in the factory):
e, err := expr.Compile(`video_rating >= 7.0 and not (video_source == "CAM")`)
if err != nil {
    return nil, fmt.Errorf("my_plugin: invalid condition: %w", err)
}

// Evaluate per entry (e.g. in Process):
data := interp.EntryData(entry)
ok, err := e.Eval(data)
```

### Operators

| Form | Example |
|------|---------|
| Equality | `series_episode_id == ''` |
| Inequality | `video_source != "CAM"` |
| Numeric comparison | `video_rating >= 7.0`, `torrent_seeds > 5` |
| String contains | `video_genres contains "Documentary"` |
| Regex match | `title matches "S[0-9]+E[0-9]+"` |
| Logical AND | `a > 1 and b != "x"` or `a > 1 && b != "x"` |
| Logical OR | `a == "x" or b == "y"` or `\|\|` |
| Logical NOT | `not enriched` or `!enriched` |
| Grouping | `not (a == "CAM" or a == "TS")` |
| Boolean literals | `true`, `false` |

### Built-in functions

| Function | Returns | Example |
|----------|---------|---------|
| `now()` | current `time.Time` | `series_next_air_date > now()` |
| `daysago(n)` | `time.Time` n days in the past | `published_date > daysago(7)` |
| `weeksago(n)` | `time.Time` n weeks in the past | `series_first_air_date > weeksago(4)` |
| `monthsago(n)` | `time.Time` n months in the past | `video_year > monthsago(12)` |
| `date("yyyy-mm-dd")` | parsed `time.Time` | `series_episode_air_date > date("2024-01-01")` |

Time fields (`series_first_air_date`, `series_next_air_date`, etc.) stored as
`time.Time` in entry fields can be compared directly with `now()` and the
date helper functions using `<`, `<=`, `>`, `>=`.

Unknown field names evaluate to an empty string (lenient — no error).

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
and convenience keys (`Title`, `URL`, `Task`, `raw_title`, `url`, `original_url`).

`interp.EntryDataWithState(e)` additionally includes `state` and
`reject_reason`, useful for notification templates that need to report
the outcome.

**Format specifiers:** `{field:format}` maps to `fmt.Sprintf("%format", value)`.
For example `{series_season:02d}` formats the season number as a zero-padded
two-digit integer.

**Backward compatibility:** Patterns containing `{{` are compiled as Go
templates directly (e.g. `{{.Title}}`), so old-style templates continue to work.

### Template helper functions

When using Go template syntax (`{{...}}`), the following helper functions are
available in every pattern (registered via `internal/template.FuncMap()`):

**String functions:**

| Function | Signature | Example |
|----------|-----------|---------|
| `upper` | `(s string) string` | `{{.title \| upper}}` |
| `lower` | `(s string) string` | `{{.title \| lower}}` |
| `trimspace` | `(s string) string` | `{{.raw_title \| trimspace}}` |
| `replace` | `(old, new, s string) string` | `{{.title \| replace "." " "}}` |
| `slice` | `(from, to int, s string) string` | `{{.published_date \| slice 0 4}}` (first 4 chars) |
| `join` | `(sep string, items any) string` | `{{.video_genres \| join ", "}}` |
| `hasSuffix` | `(suffix, s string) bool` | `{{if hasSuffix ".rar" .file_name}}...{{end}}` |
| `hasPrefix` | `(prefix, s string) bool` | `{{if hasPrefix "The " .title}}...{{end}}` |
| `contains` | `(sub, s string) bool` | `{{if contains "HDTV" .title}}...{{end}}` |
| `default` | `(fallback, x any) any` | `{{.video_language \| default "en"}}` |

**Path sanitization:**

| Function | Signature | Example |
|----------|-----------|---------|
| `scrub` | `(s string) string` | `{{.title \| scrub}}` — removes chars invalid on any OS |
| `scrubwin` | `(s string) string` | `{{.title \| scrubwin}}` — removes chars invalid on Windows |

**Date/time functions:**

| Function | Signature | Example |
|----------|-----------|---------|
| `now` | `() time.Time` | `{{now \| formatdate "2006"}}` |
| `daysago` | `(n int) time.Time` | `{{daysago 7}}` |
| `parsedate` | `(s string) time.Time` | `{{parsedate "2024-01-15"}}` |
| `formatdate` | `(layout string, t time.Time) string` | `{{.series_episode_air_date \| formatdate "2006-01-02"}}` |
| `before` | `(a, b time.Time) bool` | `{{if before .series_next_air_date now}}...{{end}}` |
| `after` | `(a, b time.Time) bool` | `{{if after .series_first_air_date (daysago 30)}}...{{end}}` |

The `scrub` function replaces characters that are invalid on Windows or Linux
filesystems (control characters, `<>:"/\|?*`, etc.) with `_`. Use it when
building file paths from entry titles. `scrubwin` applies stricter Windows rules
including reserved names (`CON`, `NUL`, `COM1`, etc.).

---

## Starlark configuration built-ins

Config scripts are evaluated as [Starlark](https://github.com/google/starlark-go)
(a Python-like language). The following built-ins are available in every config file.

### Node constructors

```python
node = input("plugin_name", key=value, ...)
node = process("plugin_name", upstream=node_or_list, key=value, ...)
output("plugin_name", upstream=node_or_list, key=value, ...)
```

`upstream=` accepts a single node handle, a list of node handles (fan-in merge), or
the return value of `merge(...)`.

### merge()

```python
merged = merge(node_a, node_b, ...)
```

Convenience alias — returns a list of node handles that, when passed as `upstream=`,
causes the executor to merge and deduplicate entries from all listed nodes.

### pipeline()

```python
pipeline("my_pipeline", schedule="1h")
```

Seals all `input()`, `process()`, and `output()` calls made since the last
`pipeline()` call into a named, schedulable pipeline. `schedule` accepts Go duration
strings (`"30m"`, `"1h"`, `"24h"`) or a 5-field cron expression.

### route()

```python
routes = route(upstream_node,
    port_name = "boolean expression",
    ...)
downstream = process("plugin", upstream=routes.port_name, ...)
```

Routes entries to named ports based on ordered boolean conditions. See
[Conditional branching with route()](#conditional-branching-with-route) for details.

### env()

```python
value = env("ENV_VAR_NAME")              # error if variable is not set
value = env("ENV_VAR_NAME", default="") # use default if not set
```

Reads an environment variable at config-load time. Use this to inject secrets
and deployment-specific values without hardcoding them in config files.

### load()

```python
load("./shared.star", "my_function", "MY_CONST")
```

Standard Starlark `load` statement — imports symbols from another `.star` file in
the same directory (or any path relative to the config file). Use it to share
helper functions and constants across multiple pipeline configs.

### User-defined pipeline functions

Functions that wrap reusable pipeline patterns can be annotated so the visual
editor surfaces them as palette entries with typed form fields:

```python
# Fetches a tagged RSS feed and rejects entries below a quality floor.
# pipeliner:param url           type=string  RSS feed URL
# pipeliner:param min_quality   type=string  Minimum quality, e.g. "720p"
def tagged_rss(upstream, url, min_quality="720p"):
    src  = input("rss", url=url)
    meta = process("metainfo_file", upstream=src)
    flt  = process("condition", upstream=meta,
                   reject='video_resolution != "" and video_resolution < "' + min_quality + '"')
    return flt
```

The `# pipeliner:param name [type=TYPE] hint text` annotation syntax:

| Annotation | Effect |
|------------|--------|
| `# pipeliner:param name hint` | String parameter named `name` with the given hint |
| `# pipeliner:param name type=int hint` | Typed parameter — any `FieldType` value is accepted |
| `# pipeliner:param name type=bool hint` | Boolean parameter |
| `# pipeliner:param name type=list hint` | List-of-strings parameter |

The `upstream` parameter is always treated as a wiring argument and is excluded
from the generated schema. Parameters with default values are optional; those
without are required.

---

## List and search plugin patterns

### Marking a source as a list plugin

Set `IsListPlugin: true` in the descriptor when your source plugin's entry
titles can serve as a dynamic name list for `series`, `movies`, or `discover`:

```go
plugin.Register(&plugin.Descriptor{
    PluginName:   "my_list_source",
    Role:         plugin.RoleSource,
    IsListPlugin: true,
    // ...
})
```

### Custom cache key for list sources

When a source plugin is used as a list source, the executor caches its output
per-source. By default the cache key is `Name()`. Implement `CacheKeyer` to
provide a more specific key (e.g. including URL or user ID) so that multiple
instances of the same plugin type with different configs do not share a cache
entry:

```go
type CacheKeyer interface {
    CacheKey() string
}

func (p *myListSource) CacheKey() string {
    return fmt.Sprintf("my_list_source:%s", p.userID)
}
```

### Title matching: the match package

Plugins that match entry titles against a list (e.g. series, movies) should
use `internal/match` rather than rolling their own string comparison:

```go
import "github.com/brunoga/pipeliner/internal/match"

// Normalise a raw title before any comparison.
norm := match.Normalize(e.Title)  // lowercases, collapses dots/hyphens/underscores

// Check a single title against a TitleEntry from the list.
te := match.NewTitleEntry("Breaking Bad", 2008)  // Norm is set automatically
if match.FuzzyEntry(norm, entry.ReleaseYear(e), te) {
    // matched — Fuzzy handles exact, glob, and single-char Levenshtein
}

// Check year compatibility independently.
ok := match.YearsCompatible(2008, 2009) // true — within 1 year tolerance
```

**Key functions:**

| Function | Signature | Description |
|----------|-----------|-------------|
| `Normalize` | `(s string) string` | Lowercase + collapse separators into spaces |
| `Fuzzy` | `(a, b string) bool` | Exact, glob (`filepath.Match`), or Levenshtein ≤ 1 on normalised strings |
| `FuzzyEntry` | `(norm string, year int, t TitleEntry) bool` | Fuzzy title match AND year compatibility — the standard entry-point for list matching |
| `NewTitleEntry` | `(title string, year int) TitleEntry` | Create a `TitleEntry` (normalises title automatically) |
| `YearsCompatible` | `(a, b int) bool` | True when years are within 1 of each other, or either is 0 (unknown) |

**Important:** always use `entry.ReleaseYear(e)` (not `e.GetInt("video_year")` directly) to
extract the year for matching — it handles multiple numeric types and returns 0 when absent.

### Instantiating list and search sub-plugins from config

When a plugin accepts a `list=` or `search=` config key, use `plugin.MakeListPlugin`
and the search counterpart to instantiate items from the raw config value:

```go
// In Factory: build list sources from cfg["list"]
items, _ := cfg["list"].([]any)
for _, item := range items {
    src, err := plugin.MakeListPlugin(item, db)
    if err != nil {
        return nil, fmt.Errorf("my_plugin: list: %w", err)
    }
    p.listSources = append(p.listSources, src)
}
```

`MakeListPlugin(item, db)` accepts a plugin name string, a `map[string]any` with a
`"name"` key, or a `*plugin.NodePipeline` (produced when a mini-pipeline node handle
is passed in config). It returns a logging-wrapped `SourcePlugin`.

### ResolveDynamicList

Plugins that accept a `list=` config key (e.g. `series`, `movies`) use
`plugin.ResolveDynamicList` to merge static title entries with dynamically
fetched ones:

```go
titles := plugin.ResolveDynamicList(ctx, tc, p.listSources, p.staticTitles,
    func(src string) ([]match.TitleEntry, bool) { return p.listCache.Get(src) },
    func(src string, entries []match.TitleEntry) { p.listCache.Set(src, entries) },
)
```

This handles per-source caching, logging, and merging automatically. Static
entries come first; dynamic entries follow.

---

## Advanced utilities

### Series episode parsing (`internal/series`)

The `internal/series` package parses TV series and episode information from
release titles and provides a persistent tracker for download history. Use it
when writing series-aware processors.

```go
import "github.com/brunoga/pipeliner/internal/series"

// Parse episode metadata from a release title:
ep, ok := series.Parse("Breaking.Bad.S02E05.1080p.BluRay-GROUP")
if !ok {
    e.Reject("series: title did not parse as episode")
    return
}
// ep.SeriesName → "Breaking Bad"
// ep.Season → 2, ep.Episode → 5
// ep.Proper, ep.Repack, ep.Service, ep.Container
// ep.Quality (quality.Quality)

epID := series.EpisodeID(ep) // → "S02E05"

norm := series.NormalizeName(ep.SeriesName) // title normalisation for comparison
```

**`series.Tracker`** — a persistent tracker for recording which episodes have
been downloaded, backed by a `store.Bucket`:

```go
tracker := series.NewTracker(db.Bucket("series"))

// Check if already downloaded:
if tracker.IsSeen(ep.SeriesName, epID) {
    e.Reject("series: already downloaded")
    return
}

// Record after a successful download (e.g. in Commit):
tracker.Mark(series.Record{
    SeriesName:   ep.SeriesName,
    EpisodeID:    epID,
    DownloadedAt: time.Now().UTC(),
    Quality:      ep.Quality,
})

// Query history:
rec, ok := tracker.Latest(ep.SeriesName)  // most recent downloaded episode
rec, ok  = tracker.Earliest(ep.SeriesName) // oldest downloaded episode
```

**`series.Episode` struct fields:**

| Field | Type | Description |
|-------|------|-------------|
| `SeriesName` | string | Parsed show name (e.g. `"Breaking Bad"`) |
| `Season` | int | Season number |
| `Episode` | int | Episode number |
| `DoubleEpisode` | int | Second episode number for double releases (S01E01E02 → 2) |
| `IsDate` | bool | True when identified by air date |
| `Year/Month/Day` | int | For date-format episodes |
| `Proper` | bool | Release is a PROPER (corrected) version |
| `Repack` | bool | Release is a REPACK |
| `Service` | string | Streaming service tag (e.g. `"AMZN"`, `"ATVP"`) |
| `Container` | string | File container (e.g. `"mkv"`) |
| `Quality` | quality.Quality | Parsed quality object |

### Quality parsing (`internal/quality`)

The `internal/quality` package parses video quality attributes from release
titles and provides a spec-matching system. Use it when your plugin needs to
compare or filter by quality (resolution, source, codec, audio, HDR).

```go
import "github.com/brunoga/pipeliner/internal/quality"

// Parse quality from a release title:
q := quality.Parse("Show.S01E01.1080p.BluRay.x265.HEVC-GROUP")
// q.ResolutionName() → "1080p", q.SourceName() → "BluRay"

// Compare two qualities:
if q.Better(storedQuality) {
    e.Accept() // accept the upgrade
}

// Parse a user-supplied spec string:
spec, err := quality.ParseSpec("720p+ webrip+") // "720p or better, web-rip or better"
if err != nil {
    return nil, fmt.Errorf("my_plugin: invalid quality spec: %w", err)
}
if spec.Matches(q) {
    e.Accept()
}
```

**Key API:**

| Function / Method | Description |
|-------------------|-------------|
| `quality.Parse(title string) Quality` | Extract quality attributes from a release title |
| `quality.ParseSpec(s string) (Spec, error)` | Parse a user-supplied quality spec string |
| `Quality.Better(other Quality) bool` | True when this quality is strictly better |
| `Quality.String() string` | Human-readable summary of detected quality |
| `Quality.ResolutionName() string` | E.g. `"1080p"`, `"2160p"`, `""` |
| `Quality.SourceName() string` | E.g. `"BluRay"`, `"WEBRip"`, `""` |
| `Spec.Matches(q Quality) bool` | True when `q` falls within the spec's range |

**Spec syntax:** space-separated tokens, each constraining one dimension.
A `+` suffix means "this or better" (min only). A `min-max` range sets both
bounds. Examples: `"1080p"`, `"720p-1080p"`, `"720p+ webrip+"`, `"2160p bluray"`.

## Internal plugins

Mark a plugin `Internal: true` when it is an implementation detail of a
higher-level built-in and should not be usable directly by users in config:

```go
plugin.Register(&plugin.Descriptor{
    PluginName: "route_selector",
    Role:       plugin.RoleProcessor,
    Internal:   true,
    Factory:    newSelectorPlugin,
})
```

Internal plugins:
- Are registered in the global registry so the executor can instantiate them.
- Are hidden from the `pipeliner list-plugins` output and the visual editor palette.
- Cannot be referenced directly in user config files.

The `route_selector` plugin (created automatically by the `route()` Starlark
builtin to fan entries out to named ports) is the canonical example.

---

## Registering in main.go

Add a blank import of your plugin package to `cmd/pipeliner/main.go`:

```go
// Source plugins
_ "github.com/brunoga/pipeliner/plugins/source/my_source"

// Processor plugins
_ "github.com/brunoga/pipeliner/plugins/processor/filter/my_filter"
_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/my_metainfo"
_ "github.com/brunoga/pipeliner/plugins/processor/modify/my_modify"

// Sink plugins
_ "github.com/brunoga/pipeliner/plugins/sink/my_sink"
```

The blank import causes the `init()` function to run, which calls
`plugin.Register`. The order of imports does not matter.

**Inline factory and validate for trivial plugins:**

For zero-config plugins, the factory and validate functions can be written
inline as lambdas directly in the `Descriptor`, avoiding the need for separate
named functions:

```go
func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "accept_all",
        Description: "accept every undecided entry",
        Role:        plugin.RoleProcessor,
        Factory: func(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
            return &acceptAllPlugin{}, nil
        },
        Validate: func(cfg map[string]any) []error {
            return plugin.OptUnknownKeys(cfg, "accept_all")
        },
    })
}
```

### Plugin directory conventions

Place new plugins under the appropriate sub-directory:

| Path | Purpose |
|------|---------|
| `plugins/source/<name>/` | Source plugins (`RoleSource`) |
| `plugins/processor/filter/<name>/` | Filter and dedup processors |
| `plugins/processor/metainfo/<name>/` | Metadata enrichment processors |
| `plugins/processor/modify/<name>/` | Field mutation processors |
| `plugins/sink/<name>/` | Sink plugins (`RoleSink`) |

Each plugin package should contain at minimum `<name>.go` (implementation),
`<name>_test.go` (tests), and `README.md` (user-facing documentation).

---

## Testing plugins

Every plugin package must have a `*_test.go` file. Unit-test the plugin's
core logic in isolation; no real network calls, no real files.

### Standard test setup

```go
package myplugin

import (
    "context"
    "log/slog"
    "testing"

    "github.com/brunoga/pipeliner/internal/entry"
    "github.com/brunoga/pipeliner/internal/plugin"
    "github.com/brunoga/pipeliner/internal/store"
)

// makeDB opens an in-memory SQLite store and registers a cleanup function.
func makeDB(t *testing.T) *store.SQLiteStore {
    t.Helper()
    db, err := store.OpenSQLite(":memory:")
    if err != nil {
        t.Fatalf("OpenSQLite: %v", err)
    }
    t.Cleanup(func() { db.Close() })
    return db
}

// makeTC returns a minimal TaskContext suitable for tests.
func makeTC(name string) *plugin.TaskContext {
    return &plugin.TaskContext{Name: name, Logger: slog.Default()}
}

// makePlugin constructs a plugin via the exported factory and asserts no error.
func makePlugin(t *testing.T, cfg map[string]any) *myPlugin {
    t.Helper()
    db := makeDB(t)
    p, err := newPlugin(cfg, db)
    if err != nil {
        t.Fatalf("newPlugin: %v", err)
    }
    return p.(*myPlugin)
}
```

### Testing a ProcessorPlugin

```go
func TestAcceptsHighScore(t *testing.T) {
    p := makePlugin(t, map[string]any{"threshold": 7.0})
    e := entry.New("Good Show", "http://example.com/a")
    e.Set("my_score", 8.5)

    out, err := p.Process(context.Background(), makeTC("test"), []*entry.Entry{e})
    if err != nil {
        t.Fatal(err)
    }
    if len(out) != 1 || !out[0].IsAccepted() {
        t.Errorf("expected entry to be accepted")
    }
}

func TestRejectsBelowThreshold(t *testing.T) {
    p := makePlugin(t, map[string]any{"threshold": 7.0})
    e := entry.New("Bad Show", "http://example.com/b")
    e.Set("my_score", 4.0)

    p.Process(context.Background(), makeTC("test"), []*entry.Entry{e}) //nolint:errcheck
    if !e.IsRejected() {
        t.Errorf("expected entry to be rejected")
    }
}
```

### Testing a SourcePlugin

```go
func TestGenerate(t *testing.T) {
    p := makePlugin(t, map[string]any{"url": server.URL})
    entries, err := p.Generate(context.Background(), makeTC("test"))
    if err != nil {
        t.Fatal(err)
    }
    if len(entries) == 0 {
        t.Error("expected entries")
    }
}
```

### Testing a SinkPlugin

```go
func TestConsumeCallsWebhook(t *testing.T) {
    var called bool
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        called = true
    }))
    defer srv.Close()

    p := makePlugin(t, map[string]any{"url": srv.URL})
    e := entry.New("Title", "http://example.com/a")
    e.Accept()

    err := p.Consume(context.Background(), makeTC("test"), []*entry.Entry{e})
    if err != nil {
        t.Fatal(err)
    }
    if !called {
        t.Error("expected webhook to be called")
    }
}

func TestConsumeDryRunSkips(t *testing.T) {
    var called bool
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        called = true
    }))
    defer srv.Close()

    p := makePlugin(t, map[string]any{"url": srv.URL})
    e := entry.New("Title", "http://example.com/a")
    e.Accept()

    tc := &plugin.TaskContext{Name: "test", DryRun: true, Logger: slog.Default()}
    p.Consume(context.Background(), tc, []*entry.Entry{e}) //nolint:errcheck
    if called {
        t.Error("DryRun should skip side effects")
    }
}
```

### Testing CommitPlugin contracts

When a plugin implements `CommitPlugin`, write tests that verify:

1. `Process()` does **not** persist state.
2. `Commit()` **does** persist state.
3. Entries failed by a sink are **not** committed.

```go
func TestProcessDoesNotPersist(t *testing.T) {
    p := makePlugin(t, nil)
    e := entry.New("Test", "http://example.com/a")
    tc := makeTC("test")

    p.Process(context.Background(), tc, []*entry.Entry{e})

    // Run a second Process call — entry should NOT be rejected yet
    e2 := entry.New("Test", "http://example.com/a")
    out, _ := p.Process(context.Background(), tc, []*entry.Entry{e2})
    if len(out) == 0 || out[0].IsRejected() {
        t.Error("Process should not persist; entry should not be rejected before Commit")
    }
}

func TestCommitPersists(t *testing.T) {
    p := makePlugin(t, nil)
    tc := makeTC("test")
    e := entry.New("Test", "http://example.com/a")
    e.Accept()

    p.Commit(context.Background(), tc, []*entry.Entry{e})

    // After Commit, the same entry should now be rejected by Process
    e2 := entry.New("Test", "http://example.com/a")
    p.Process(context.Background(), tc, []*entry.Entry{e2})
    if !e2.IsRejected() {
        t.Error("entry should be rejected after Commit")
    }
}
```

### Testing the Validate function

```go
func TestValidateMissingURL(t *testing.T) {
    errs := validate(map[string]any{})
    if len(errs) == 0 {
        t.Error("expected error for missing url")
    }
}

func TestValidateUnknownKey(t *testing.T) {
    errs := validate(map[string]any{"url": "http://x.com", "typo": true})
    if len(errs) == 0 {
        t.Error("expected error for unknown key")
    }
}
```

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
`plugins/source/rss/` for a real-world source, `plugins/sink/transmission/` for a
real-world sink, and `plugins/processor/filter/seen/` for a real-world `CommitPlugin`
implementation.*
