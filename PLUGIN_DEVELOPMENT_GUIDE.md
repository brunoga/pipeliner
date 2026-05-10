# Plugin Development Guide

This guide covers everything you need to write a new pipeliner plugin: interfaces, registration, the entry model, persistence, caching, config validation, and the patterns for plugins that use other plugins as sub-components.

## Table of Contents

1. [Concepts](#concepts)
2. [Plugin phases and execution model](#plugin-phases-and-execution-model)
3. [Registering a plugin](#registering-a-plugin)
4. [The Entry data model](#the-entry-data-model)
5. [Standard field system](#standard-field-system)
6. [TaskContext](#taskcontext)
7. [Plugin interfaces by phase](#plugin-interfaces-by-phase)
   - [InputPlugin](#inputplugin)
   - [MetainfoPlugin and BatchMetainfoPlugin](#metainfoplugin-and-batchmetainfoplugin)
   - [FilterPlugin](#filterplugin)
   - [ModifyPlugin](#modifyplugin)
   - [OutputPlugin](#outputplugin)
   - [LearnPlugin](#learnplugin)
   - [SearchPlugin](#searchplugin)
8. [Config validation](#config-validation)
9. [Persistence: the store and bucket API](#persistence-the-store-and-bucket-api)
10. [Caching](#caching)
11. [String interpolation](#string-interpolation)
12. [Plugins that use other plugins](#plugins-that-use-other-plugins)
13. [Combining multiple interfaces](#combining-multiple-interfaces)
14. [Registering in main.go](#registering-in-maingo)
15. [Complete examples](#complete-examples)

---

## Concepts

A pipeline **task** is an ordered sequence of plugins. Each plugin participates in exactly one phase. The task engine collects entries from all input plugins concurrently, then runs metainfo, filter, and modify plugins **in config-file order** (interleaved with each other), then passes accepted entries to all output plugins concurrently, and finally runs learn plugins to persist decisions.

A plugin is a Go struct that:
- implements the `plugin.Plugin` base interface (`Name() string`, `Phase() Phase`)
- implements one additional phase-specific interface (e.g. `InputPlugin`, `FilterPlugin`)
- is constructed by a **factory function** registered in a `plugin.Descriptor`
- is registered via an `init()` function so it can be referenced by name in config files

---

## Plugin phases and execution model

| Phase | Interface | Execution | Receives |
|-------|-----------|-----------|----------|
| `input` | `InputPlugin` | Concurrent — all inputs run in parallel | — |
| `metainfo` | `MetainfoPlugin` / `BatchMetainfoPlugin` | Serial per entry (batch if implemented) | All entries from input |
| `filter` | `FilterPlugin` / `BatchFilterPlugin` | Serial per entry (batch if implemented) | All entries |
| `modify` | `ModifyPlugin` | Serial per entry | Undecided + Accepted entries |
| *(dedup)* | *(automatic)* | Built-in, after all processing plugins | Accepted entries with series/movie fields |
| `output` | `OutputPlugin` | Serial — plugins run in config order; each receives only entries still accepted at that point | Accepted entries only |
| `learn` | `LearnPlugin` | Serial | Accepted entries only |
| `from` | `SearchPlugin` / `InputPlugin` | Sub-plugins called by `series`, `movies`, `discover` | — |

**Execution order:**
- The input phase is fully concurrent. Do not share mutable state between input plugin calls unless protected by a mutex.
- Metainfo, filter, and modify plugins run **in config-file order**, interleaved with each other. A filter placed immediately after a metainfo plugin in the config will see the fields that metainfo plugin set.
- Input results are deduplicated by URL before the processing pipeline begins.
- Output plugins run in config order. Each receives only entries still accepted at the time it runs. An output plugin may call `e.Fail("reason")` to prevent that entry from reaching subsequent output plugins and the learn phase — useful for required outputs where failure means the download should be retried.

**Automatic episode/movie deduplication:**

After all processing plugins complete, the task engine automatically deduplicates accepted entries so that at most one copy of each episode or movie reaches the output phase. This means filter plugins (`series`, `premiere`, `movies`) should accept **all** qualifying entries — including multiple quality variants of the same item from different sources — and let the engine pick the best one.

The winning entry is chosen by:
1. **Seed tier** — entries with 2+ seeds always beat entries with exactly 1 seed
2. **Resolution** — higher resolution wins within the same seed tier
3. **Seeds** — more seeds wins when tier and resolution are equal

Entries are keyed by `title` + `series_episode_id` for episodes, and `title` + `video_year` for movies. Entries without these fields are unaffected.

For movies, 3D and non-3D versions are keyed separately (`title|year` vs `title|year|3d`) so both are downloaded independently rather than competing. Within a group of 3D candidates, the `Quality.Better()` comparison uses `Format3D` as the primary discriminator (BD3D > Full > Half), with resolution and other dimensions as tie-breakers.

**Implication for stateful plugins:** Do not write tracking state (SQLite) in `Filter` — do it in `Learn`. `Learn` runs after output and receives only the entries that survived dedup, so only the winning copy is recorded as downloaded.

---

## Registering a plugin

Every plugin is registered by calling `plugin.Register` inside an `init()` function.

```go
package myplugin

import (
    "github.com/brunoga/pipeliner/internal/plugin"
    "github.com/brunoga/pipeliner/internal/store"
)

func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "my_plugin",          // referenced in config files
        Description: "one-line description shown by list-plugins",
        PluginPhase: plugin.PhaseFilter,
        Factory:     newPlugin,
        Validate:    validate,             // optional but recommended
    })
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    // construct and return your plugin
}

func validate(cfg map[string]any) []error {
    // validate config fields; see Config validation section
}
```

`Register` panics if the same `PluginName` is registered twice. The name must be unique across the entire binary.

### Descriptor fields

| Field | Type | Description |
|-------|------|-------------|
| `PluginName` | `string` | Name used in config files and logs |
| `Description` | `string` | Short description for `pipeliner list-plugins` |
| `PluginPhase` | `Phase` | Which pipeline phase this plugin participates in |
| `Factory` | `func(map[string]any, *store.SQLiteStore) (Plugin, error)` | Constructor called at task build time |
| `Validate` | `func(map[string]any) []error` | Optional config validator called by `pipeliner check` |

---

## The Entry data model

`entry.Entry` is the core unit that flows through the pipeline.

```go
type Entry struct {
    Title        string         // human-readable name
    URL          string         // canonical download URL (may be updated by modify plugins)
    OriginalURL  string         // URL as received from input; never mutated
    State        State          // Undecided | Accepted | Rejected | Failed
    RejectReason string
    FailReason   string
    Task         string         // owning task name, set by the engine
    Fields       map[string]any // arbitrary metadata bag
}
```

### State transitions

```go
e.Accept()              // Undecided → Accepted (no-op if already Rejected)
e.Reject("reason")      // any → Rejected (always wins)
e.Fail("reason")        // any → Failed

e.IsUndecided()
e.IsAccepted()
e.IsRejected()
e.IsFailed()
```

`Reject` always wins. Once an entry is rejected it cannot be accepted. `Accept` is a no-op if the entry is already rejected.

### Metadata fields

Use the typed accessors to read fields set by other plugins:

```go
e.Set("my_field", someValue)       // store any value

e.GetString("title")               // "" if absent or wrong type
e.GetInt("series_season")          // 0 if absent or wrong type; handles int/int64/float64
e.GetBool("series_proper")         // false if absent or wrong type
e.GetTime("torrent_creation_date") // zero time if absent or wrong type

v, ok := e.Get("torrent_seeds")    // retrieve as any with presence check
```

Field names are lowercase with underscores by convention. If your plugin enriches entries, document every field it sets. Prefer standard field names and setter methods over raw `e.Set` calls — see [Standard field system](#standard-field-system).

---

## Standard field system

Pipeliner uses a **tiered standard metadata field system**. Instead of setting arbitrary provider-specific keys, plugins set fields via typed setter methods that write well-known field names. This ensures that conditions, path patterns, and templates work the same way regardless of which provider enriched an entry.

### Info type hierarchy

```
GenericInfo          (title, description, published_date, enriched)
└── VideoInfo        (video_year, video_language, video_genres, …)
    ├── MovieInfo    (movie_tagline)
    └── SeriesInfo   (series_season, series_episode, series_network, …)

TorrentInfo          (torrent_info_hash, torrent_seeds, …)   — embeds GenericInfo
FileInfo             (file_name, file_location, …)            — embeds GenericInfo
RSSInfo              (rss_feed, rss_guid, rss_link, …)        — embeds GenericInfo
```

### Setter methods

Use the typed setters on `*entry.Entry` to write standard fields. Only non-zero values are written, so partial updates are safe.

```go
// Tier 1 — universal
e.SetGenericInfo(entry.GenericInfo{Title: "Breaking Bad", Enriched: true})

// Tier 2 — video (movies and series)
e.SetVideoInfo(entry.VideoInfo{
    GenericInfo: entry.GenericInfo{Title: "Breaking Bad", Enriched: true},
    Year:        2008,
    Language:    "English",
    Genres:      []string{"Drama", "Crime"},
    Rating:      9.5,
})

// Tier 3a — movie-specific
e.SetMovieInfo(entry.MovieInfo{
    VideoInfo: entry.VideoInfo{...},
    Tagline:   "Change the equation",
})

// Tier 3b — series-specific
e.SetSeriesInfo(entry.SeriesInfo{
    VideoInfo:  entry.VideoInfo{...},
    Season:     2,
    Episode:    5,
    EpisodeID:  "S02E05",
    Network:    "AMC",
    Status:     "Ended",
})

// Torrent leaf
e.SetTorrentInfo(entry.TorrentInfo{
    InfoHash:  "abc123...",
    FileSize:  1073741824,
    Seeds:     42,
    Private:   false,
})

// File leaf
e.SetFileInfo(entry.FileInfo{
    Filename: "movie.mkv",
    Location: "/downloads/movie.mkv",
    FileSize: 2147483648,
})

// RSS leaf
e.SetRSSInfo(entry.RSSInfo{
    Feed: "https://example.com/feed",
    GUID: "item-guid-123",
    Link: "https://example.com/item/1",
})
```

### Standard field names

All field name constants are defined in `internal/entry/info.go`.

**GenericInfo (no prefix):**

| Constant | Field name | Type |
|----------|-----------|------|
| `FieldTitle` | `title` | string |
| `FieldDescription` | `description` | string |
| `FieldPublishedDate` | `published_date` | string |
| `FieldEnriched` | `enriched` | bool |

**VideoInfo (`video_` prefix):**

| Constant | Field name | Type |
|----------|-----------|------|
| `FieldVideoYear` | `video_year` | int |
| `FieldVideoLanguage` | `video_language` | string |
| `FieldVideoOriginalTitle` | `video_original_title` | string |
| `FieldVideoCountry` | `video_country` | string |
| `FieldVideoGenres` | `video_genres` | []string |
| `FieldVideoRating` | `video_rating` | float64 |
| `FieldVideoPoster` | `video_poster` | string |
| `FieldVideoCast` | `video_cast` | []string |
| `FieldVideoContentRating` | `video_content_rating` | string |
| `FieldVideoRuntime` | `video_runtime` | int |
| `FieldVideoTrailers` | `video_trailers` | []string |
| `FieldVideoAliases` | `video_aliases` | []string |
| `FieldVideoImdbID` | `video_imdb_id` | string |
| `FieldVideoQuality` | `video_quality` | string |
| `FieldVideoResolution` | `video_resolution` | string |
| `FieldVideoSource` | `video_source` | string |
| `FieldVideoIs3D` | `video_is_3d` | bool | `true` when any 3D format marker is detected; derived from `Quality.Format3D` |
| `FieldVideoPopularity` | `video_popularity` | float64 |
| `FieldVideoVotes` | `video_votes` | int |

**MovieInfo (`movie_` prefix):**

| Constant | Field name | Type |
|----------|-----------|------|
| `FieldMovieTagline` | `movie_tagline` | string |

**SeriesInfo (`series_` prefix):**

| Constant | Field name | Type |
|----------|-----------|------|
| `FieldSeriesSeason` | `series_season` | int |
| `FieldSeriesEpisode` | `series_episode` | int |
| `FieldSeriesEpisodeID` | `series_episode_id` | string |
| `FieldSeriesNetwork` | `series_network` | string |
| `FieldSeriesStatus` | `series_status` | string |
| `FieldSeriesFirstAirDate` | `series_first_air_date` | time.Time |
| `FieldSeriesLastAirDate` | `series_last_air_date` | time.Time |
| `FieldSeriesNextAirDate` | `series_next_air_date` | time.Time |
| `FieldSeriesEpisodeTitle` | `series_episode_title` | string |
| `FieldSeriesEpisodeDescription` | `series_episode_description` | string |
| `FieldSeriesEpisodeAirDate` | `series_episode_air_date` | time.Time |
| `FieldSeriesEpisodeImage` | `series_episode_image` | string |
| `FieldSeriesService` | `series_service` | string |
| `FieldSeriesProper` | `series_proper` | bool |
| `FieldSeriesRepack` | `series_repack` | bool |
| `FieldSeriesDoubleEpisode` | `series_double_episode` | int |

**TorrentInfo (`torrent_` prefix):**

| Constant | Field name | Type |
|----------|-----------|------|
| `FieldTorrentLinkType` | `torrent_link_type` | string (`"torrent"` or `"magnet"`) |
| `FieldTorrentInfoHash` | `torrent_info_hash` | string |
| `FieldTorrentFileSize` | `torrent_file_size` | int64 |
| `FieldTorrentFileCount` | `torrent_file_count` | int |
| `FieldTorrentFiles` | `torrent_files` | []string |
| `FieldTorrentSeeds` | `torrent_seeds` | int |
| `FieldTorrentLeechers` | `torrent_leechers` | int |
| `FieldTorrentAnnounce` | `torrent_announce` | string |
| `FieldTorrentAnnounceList` | `torrent_announce_list` | []string |
| `FieldTorrentCreatedBy` | `torrent_created_by` | string |
| `FieldTorrentCreationDate` | `torrent_creation_date` | time.Time |
| `FieldTorrentPrivate` | `torrent_private` | bool |

**FileInfo (`file_` prefix):**

| Constant | Field name | Type |
|----------|-----------|------|
| `FieldFileName` | `file_name` | string |
| `FieldFileExtension` | `file_extension` | string |
| `FieldFileLocation` | `file_location` | string |
| `FieldFileSize` | `file_size` | int64 |
| `FieldFileModifiedTime` | `file_modified_time` | time.Time |

**RSSInfo (`rss_` prefix):**

| Constant | Field name | Type |
|----------|-----------|------|
| `FieldRSSFeed` | `rss_feed` | string |
| `FieldRSSGUID` | `rss_guid` | string |
| `FieldRSSLink` | `rss_link` | string |
| `FieldRSSEnclosureURL` | `rss_enclosure_url` | string |
| `FieldRSSEnclosureType` | `rss_enclosure_type` | string |

### Provider-specific fields

Provider-specific fields that have no standard equivalent are still set directly with `e.Set`. These include: `tvdb_id`, `tvdb_slug`, `tvdb_episode_id`, `tmdb_id`, `trakt_id`, `trakt_slug`, `trakt_tmdb_id`, `trakt_tvdb_id`, `jackett_category`, `jackett_indexer`, `series_container`, and quality sub-fields (`codec`, `audio`, `color_range`, `quality_resolution`, `quality_source`).

### The `enriched` field

External metainfo providers (TVDB, TMDb, Trakt) set `enriched = true` on success. Use it in `require` blocks to check whether metadata was found, regardless of which provider ran:

```python
plugin("require", fields=["enriched"])   # works with any provider — TVDB, TMDb, or Trakt
```

Do not key on provider-specific IDs (e.g. `tvdb_id`) for this purpose — `enriched` is provider-neutral.

### Reading standard fields in plugin code

Use the field name constants when reading fields, and the typed accessors where available:

```go
// Using the entry method with the constant:
title := e.GetString(entry.FieldTitle)
season := e.GetInt(entry.FieldSeriesSeason)
enriched := e.GetBool(entry.FieldEnriched)

// Using the constant directly in e.Get for presence check:
if _, ok := e.Get(entry.FieldTorrentSeeds); ok {
    seeds := e.GetInt(entry.FieldTorrentSeeds)
    // ...
}
```

### Partial updates

The setter methods only write non-zero values, so multiple plugins can call the same setter without overwriting each other's data. For example, `metainfo_series` sets `title` and episode fields; a subsequent `metainfo_tvdb` call sets `title`, `description`, `video_genres`, etc. without clearing the episode fields already set.

---

## TaskContext

Every plugin method receives a `*plugin.TaskContext`:

```go
type TaskContext struct {
    Name   string         // task being executed — use for bucket names
    Config map[string]any // this plugin's config block
    Logger *slog.Logger   // pre-scoped with task=, phase=, plugin= attributes
}
```

Use `tc.Logger` for all log output — it is automatically tagged with the task, phase, and plugin name. Use `tc.Name` to namespace your SQLite bucket keys so data does not bleed between tasks:

```go
bucket := db.Bucket("seen:" + tc.Name)
```

---

## Plugin interfaces by phase

### InputPlugin

```go
type InputPlugin interface {
    Plugin
    Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error)
}
```

`Run` is called once per task execution. Return a slice of new `entry.Entry` values. The engine deduplicates by URL across all inputs before the processing pipeline begins, so overlapping results from multiple inputs are harmless.

**Pattern:**

```go
func (p *myInput) Run(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
    // fetch data from external source
    var entries []*entry.Entry
    for _, item := range items {
        e := entry.New(item.Title, item.URL)
        e.Set("my_source_field", item.SomeData)
        entries = append(entries, e)
    }
    return entries, nil
}
```

Return a non-nil error only for unrecoverable failures (the task logs the error and skips this input). For best-effort partial results, log the per-item failure and continue.

---

### MetainfoPlugin and BatchMetainfoPlugin

```go
type MetainfoPlugin interface {
    Plugin
    Annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error
}

type BatchMetainfoPlugin interface {
    Plugin
    AnnotateBatch(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error
}
```

Metainfo plugins **annotate** entries with additional data. They must not change entry state (`Accept`/`Reject` are filter concerns).

`Annotate` is called once per entry, serially. If your plugin can benefit from batching — for example, making parallel HTTP requests for all entries at once — implement `BatchMetainfoPlugin` instead. The task engine calls `AnnotateBatch` when it is present.

**Pattern — simple annotation:**

```go
func (p *myMeta) Annotate(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
    ep, ok := series.Parse(e.Title)
    if !ok {
        return nil  // not applicable — leave entry untouched
    }
    e.SetSeriesInfo(entry.SeriesInfo{
        VideoInfo:  entry.VideoInfo{GenericInfo: entry.GenericInfo{Title: ep.SeriesName}},
        Season:     ep.Season,
        Episode:    ep.Episode,
        EpisodeID:  series.EpisodeID(ep),
    })
    return nil
}
```

**Pattern — batch annotation with parallelism:**

```go
func (p *myMeta) AnnotateBatch(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
    var wg sync.WaitGroup
    for _, e := range entries {
        wg.Add(1)
        go func(e *entry.Entry) {
            defer wg.Done()
            // annotate e; all entry fields are safe to write from goroutines
            // because the engine does not read them during AnnotateBatch
        }(e)
    }
    wg.Wait()
    return nil
}
```

---

### FilterPlugin

```go
type FilterPlugin interface {
    Plugin
    Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error
}
```

`Filter` is called once per entry, serially, in config order. A plugin should call `e.Accept()`, `e.Reject(reason)`, or leave the entry `Undecided`. The engine skips already-rejected or failed entries for subsequent filter plugins — once `Rejected` or `Failed`, an entry is not passed to remaining filters.

**Pattern:**

```go
func (p *myFilter) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
    // check something about the entry
    title := e.GetString(entry.FieldTitle)
    if title == "" {
        return nil  // not applicable — leave Undecided
    }
    if !p.isWanted(title) {
        e.Reject(fmt.Sprintf("my_filter: %q not in wanted list", title))
        return nil
    }
    e.Accept()
    return nil
}
```

Return a non-nil error only for unexpected internal failures (e.g. a corrupted store read). Use `e.Reject` for expected negative decisions.

**Important — do not write state in Filter:** If your plugin tracks downloads (like `series`, `premiere`, `movies`), write to SQLite in `Learn`, not `Filter`. Multiple quality variants of the same item may all be accepted by Filter; the engine deduplicates them after all processing plugins have run and only the winner reaches `Learn`. Writing in Filter would prevent those variants from being considered.

### BatchFilterPlugin

```go
type BatchFilterPlugin interface {
    Plugin
    FilterBatch(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error
}
```

An optional extension for filter plugins that can process all entries more efficiently at once — for example, by firing network requests in parallel. The task engine calls `FilterBatch` instead of `Filter` for any plugin that implements this interface.

`FilterBatch` must respect already-decided entries (`IsRejected`/`IsFailed`) and must honour context cancellation. A common pattern is to fan out per-entry work into goroutines:

```go
func (p *myPlugin) FilterBatch(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
    var wg sync.WaitGroup
    for _, e := range entries {
        if e.IsRejected() || e.IsFailed() {
            continue
        }
        wg.Add(1)
        go func(e *entry.Entry) {
            defer wg.Done()
            p.Filter(ctx, tc, e) //nolint:errcheck
        }(e)
    }
    wg.Wait()
    return nil
}
```

---

### ModifyPlugin

```go
type ModifyPlugin interface {
    Plugin
    Modify(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error
}
```

`Modify` runs in config order alongside metainfo and filter plugins. It receives Undecided and Accepted entries (Rejected/Failed entries are skipped). It transforms field values without changing acceptance state. Common uses: computing a `download_path`, normalising a title, setting metadata for downstream output plugins.

---

### OutputPlugin

```go
type OutputPlugin interface {
    Plugin
    Output(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error
}
```

`Output` is called once per task run with all entries still accepted at the time this plugin runs. Output plugins run serially in config order, so a plugin that calls `e.Fail("reason")` removes that entry from the slice passed to subsequent output plugins and from the learn phase.

Call `e.Fail("reason")` when an entry could not be delivered and should be retried on the next run. Do not call `e.Reject` in output — rejection implies the entry was undesirable, whereas failure implies a transient delivery problem.

---

### LearnPlugin

```go
type LearnPlugin interface {
    Plugin
    Learn(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error
}
```

`Learn` receives only **accepted** entries after the output phase — the task engine pre-filters so plugins don't need to guard against other states. Use it to persist decisions so they affect future runs — marking entries as seen, recording series progress, updating quality trackers.

A plugin can implement **both** `FilterPlugin` and `LearnPlugin` on the same struct (the `seen` and `series` plugins do this). The task engine calls each interface at the appropriate phase.

**Pattern:**

```go
func (p *myPlugin) Filter(_ context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
    key := e.GetString("my_key")
    if found, _ := p.db.Bucket("data:"+tc.Name).Get(key, nil); found {
        e.Reject("my_plugin: already processed")
    }
    return nil
}

func (p *myPlugin) Learn(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
    b := p.db.Bucket("data:" + tc.Name)
    for _, e := range entries {
        if !e.IsAccepted() {
            continue
        }
        _ = b.Put(e.GetString("my_key"), struct{}{})
    }
    return nil
}
```

---

### SearchPlugin

```go
type SearchPlugin interface {
    Plugin
    Search(ctx context.Context, tc *plugin.TaskContext, query string) ([]*entry.Entry, error)
}
```

Search plugins are **not** dispatched directly by the task engine. They are sub-plugins used exclusively by the `discover` input plugin. Register them with `PhaseFrom`.

`Search` receives a query string (a title to search for) and returns matching entries. An empty query string conventionally returns recent/all results from the source.

---

## Config validation

Register a `Validate` function alongside your factory. It is called by `pipeliner check` before any plugin is constructed, allowing all config errors to be reported at once.

```go
func validate(cfg map[string]any) []error {
    var errs []error
    if err := plugin.RequireString(cfg, "api_key", "my_plugin"); err != nil {
        errs = append(errs, err)
    }
    if err := plugin.OptDuration(cfg, "cache_ttl", "my_plugin"); err != nil {
        errs = append(errs, err)
    }
    if err := plugin.OptEnum(cfg, "mode", "my_plugin", "fast", "slow"); err != nil {
        errs = append(errs, err)
    }
    if err := plugin.RequireOneOf(cfg, "my_plugin", "shows", "from"); err != nil {
        errs = append(errs, err)
    }
    return errs
}
```

### Available helpers (`internal/plugin/validate.go`)

| Helper | Description |
|--------|-------------|
| `RequireString(cfg, key, plugin)` | Returns error if `cfg[key]` is absent or empty |
| `RequireOneOf(cfg, plugin, keys...)` | Returns error if none of the keys are non-empty |
| `OptDuration(cfg, key, plugin)` | Returns error if set but not a valid `time.Duration` string |
| `OptEnum(cfg, key, plugin, values...)` | Returns error if set but not one of the allowed values |

The `Validate` function runs independently of the `Factory` — it must not assume any state beyond what is in `cfg`. Validation errors are presented as `task "name" plugin "name": <message>` to the user.

---

## Persistence: the store and bucket API

Every plugin factory receives a `*store.SQLiteStore`. Use it to persist state across runs.

```go
// In your factory:
func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    return &myPlugin{db: db}, nil
}

// In your plugin method:
func (p *myPlugin) Learn(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
    bucket := p.db.Bucket("my_plugin:" + tc.Name)  // namespace by task
    for _, e := range entries {
        if e.IsAccepted() {
            _ = bucket.Put(e.URL, myRecord{Title: e.Title, SeenAt: time.Now()})
        }
    }
    return nil
}
```

### Bucket API

```go
type Bucket interface {
    Put(key string, value any) error            // JSON-encode and upsert
    Get(key string, dest any) (bool, error)     // decode into dest; (false, nil) if absent
    Delete(key string) error                    // remove; no-op if absent
    Keys() ([]string, error)                    // all keys (unordered)
    All() (map[string][]byte, error)            // all key→raw-JSON pairs (single query)
}
```

Values are JSON-encoded. Any JSON-serialisable Go value can be stored. The `dest` argument to `Get` follows the same rules as `json.Unmarshal`.

**Namespace your buckets** using the task name (`tc.Name`) to prevent data from one task bleeding into another:

```go
bucket := db.Bucket("seen:" + tc.Name)
```

---

## Caching

For data fetched from external APIs that should be reused across entries and across runs, use `internal/cache`:

```go
import "github.com/brunoga/pipeliner/internal/cache"

type myPlugin struct {
    db    *store.SQLiteStore
    cache *cache.Cache[[]MyData]  // generic; V can be any JSON-serialisable type
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    ttl := 24 * time.Hour
    // parse cfg["cache_ttl"] if present...

    c := cache.NewPersistent[[]MyData](ttl, db.Bucket("cache_my_plugin"))
    c.Preload() // bulk-load all cached entries into memory at startup

    return &myPlugin{db: db, cache: c}, nil
}

func (p *myPlugin) fetchData(ctx context.Context, key string) ([]MyData, error) {
    if data, ok := p.cache.Get(key); ok {
        return data, nil  // served from memory after Preload
    }
    data, err := callExternalAPI(ctx, key)
    if err != nil {
        return nil, err
    }
    p.cache.Set(key, data)
    return data, nil
}
```

### Choosing between `New` and `NewPersistent`

| Constructor | Survives restart | Use when |
|-------------|-----------------|----------|
| `cache.New[V](ttl)` | No | Data is cheap to re-fetch; only need per-run deduplication |
| `cache.NewPersistent[V](ttl, bucket)` | Yes | External API calls with rate limits or latency |

**Always call `Preload()` on persistent caches** in the constructor. Without it, the first access to each key in a run goes to SQLite individually (~10–30 ms each). `Preload()` loads the entire bucket in a single query so all subsequent `Get` calls are in-memory.

---

## String interpolation

Plugins that compute paths or messages from entry data should use the interpolation system:

```go
import "github.com/brunoga/pipeliner/internal/interp"

type myPlugin struct {
    pathIP *interp.Interpolator
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
    pattern, _ := cfg["path"].(string)
    if pattern == "" {
        return nil, fmt.Errorf("my_plugin: 'path' is required")
    }
    ip, err := interp.Compile(pattern)
    if err != nil {
        return nil, fmt.Errorf("my_plugin: invalid path pattern: %w", err)
    }
    return &myPlugin{pathIP: ip}, nil
}

func (p *myPlugin) Modify(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
    data := interp.EntryData(e)
    path, err := p.pathIP.Render(data)
    if err != nil {
        return fmt.Errorf("my_plugin: render path: %w", err)
    }
    e.Set("download_path", path)
    return nil
}
```

### Interpolation syntax

Users write patterns in config files using `{field}` syntax:

```python
plugin("pathfmt",
    path="/downloads/{title}/Season {series_season:02d}",
    field="download_path",
)
```

| Syntax | Meaning |
|--------|---------|
| `{field}` | Value of `field` from entry |
| `{field:fmt}` | Value formatted with Go fmt verb, e.g. `{season:02d}` |
| `{{.Field}}` | Go template syntax (backward compat) |

`interp.EntryData(e)` returns a map with both capitalised names (`Title`, `URL`) and lowercase field names (`title`, `url`) plus everything in `e.Fields`.

---

## Plugins that use other plugins

Several patterns exist for a plugin that needs to use another plugin as a sub-component.

### Loading an InputPlugin from config (`from:` pattern)

The `series` and `discover` plugins load a list of `InputPlugin` instances from a `from:` config key. Each `from` item is either a plugin name string or a map with a `name` key plus plugin-specific options.

```go
import "github.com/brunoga/pipeliner/internal/plugin"

type myPlugin struct {
    sources []plugin.InputPlugin
    db      *store.SQLiteStore
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    fromRaw, _ := cfg["from"].([]any)
    var sources []plugin.InputPlugin
    for _, item := range fromRaw {
        inp, err := plugin.MakeFromPlugin(item, db)
        if err != nil {
            return nil, fmt.Errorf("my_plugin: from: %w", err)
        }
        sources = append(sources, inp)
    }
    return &myPlugin{sources: sources, db: db}, nil
}
```

`plugin.MakeFromPlugin` handles both the string form (`"tvdb_favorites"`) and the map form (`{name: tvdb_favorites, api_key: "..."}`) transparently. It validates that the plugin is registered under `PhaseFrom` and wraps it in a logging decorator that automatically emits `info`-level start/done/duration lines on every `Run()` call — no manual logging needed. The resolved plugin receives its own `Factory` call and shares the same `db`.

**At runtime**, use `plugin.ResolveDynamicList` to handle caching, invocation, and normalization in one call:

```go
func (p *myPlugin) loadTitles(ctx context.Context, tc *plugin.TaskContext) []string {
    return plugin.ResolveDynamicList(ctx, tc, p.sources, p.staticTitles,
        func(src string) ([]string, bool) { return p.listCache.Get(src) },
        func(src string, v []string) { p.listCache.Set(src, v) },
        match.Normalize,
    )
}
```

`ResolveDynamicList` caches each from-plugin's results **independently** under a per-source key (see below), so sources with different configurations are never mixed. Cache hits are logged at debug; live fetches are logged at info via the logging wrapper.

If you need custom per-entry logic beyond title extraction, call `inp.Run()` directly — the logging wrapper in each from-plugin still fires automatically.

#### Per-source cache keys and `CacheKeyer`

By default the cache key for a from-plugin is its registered name (e.g. `"tvdb_favorites"`). When the same plugin can be configured with different parameters that produce different data — for example `trakt_list` with `list: watchlist` vs `list: ratings` — implement the `plugin.CacheKeyer` interface to return a more specific key:

```go
// CacheKey returns a key that includes the parameters that affect the data
// returned, so two instances with different config are cached independently.
func (p *myFromPlugin) CacheKey() string {
    return "my_plugin:" + p.itemType + ":" + p.listName
}
```

`ResolveDynamicList` automatically calls `CacheKey()` when available. If not implemented, it falls back to `Name()`. The `loggedFromPlugin` wrapper forwards `CacheKey()` transparently, so no special handling is needed at the call site.

### Loading a SearchPlugin from config (`via:` pattern)

The `discover` input plugin loads SearchPlugins from a `via:` key using `plugin.ResolveNameAndConfig`:

```go
func resolveSearchPlugin(item any, db *store.SQLiteStore) (plugin.SearchPlugin, error) {
    name, pluginCfg, err := plugin.ResolveNameAndConfig(item)
    if err != nil {
        return nil, err
    }
    desc, ok := plugin.Lookup(name)
    if !ok {
        return nil, fmt.Errorf("unknown search plugin %q", name)
    }
    p, err := desc.Factory(pluginCfg, db)
    if err != nil {
        return nil, fmt.Errorf("instantiate %q: %w", name, err)
    }
    sp, ok := p.(plugin.SearchPlugin)
    if !ok {
        return nil, fmt.Errorf("%q does not implement SearchPlugin", name)
    }
    return sp, nil
}
```

`ResolveNameAndConfig` handles both the string form and the map form, returning the name and config map.

### Config shape for sub-plugins

In Starlark configs, sub-plugins are expressed as either a string or a dict inside the parent's `from` or `via` list:

```python
# String form (no extra config needed)
plugin("series", **{"from": [
    "tvdb_favorites",   # use the plugin name registered in PluginName
]})

# Dict form (plugin name plus its own config)
plugin("series", **{"from": [
    {"name": "tvdb_favorites", "api_key": "...", "user_pin": "..."},
]})
```

Both forms are handled by `MakeFromPlugin` and `ResolveNameAndConfig`.

---

## Combining multiple interfaces

A single struct can implement multiple plugin interfaces. The task engine checks for each interface independently at each phase.

**FilterPlugin + LearnPlugin** is the most common combination — filter to reject previously seen entries, learn to record newly accepted ones:

```go
type seenPlugin struct{ db *store.SQLiteStore }

func (p *seenPlugin) Name() string         { return "seen" }
func (p *seenPlugin) Phase() plugin.Phase  { return plugin.PhaseFilter }

func (p *seenPlugin) Filter(_ context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
    if found, _ := p.db.Bucket("seen:"+tc.Name).Get(e.URL, nil); found {
        e.Reject("seen: already downloaded")
    }
    return nil
}

func (p *seenPlugin) Learn(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
    b := p.db.Bucket("seen:" + tc.Name)
    for _, e := range entries {
        if e.IsAccepted() {
            _ = b.Put(e.URL, struct{}{})
        }
    }
    return nil
}
```

`Phase()` must return the **filter** phase even though the plugin also implements `LearnPlugin` — the engine dispatches by interface, not by phase value, for the learn pass.

Note that `seen` keys on URL, so each URL is a unique entry — there is no multi-variant dedup concern. For plugins that track by series/movie title (like `series`, `premiere`, `movies`), writing in `Learn` rather than `Filter` is essential: multiple quality variants of the same item are all accepted by Filter and deduplicated by the engine before Learn runs, ensuring only the winner is persisted.

---

## Registering in main.go

After creating your plugin package, add a side-effect import to `cmd/pipeliner/main.go`:

```go
import (
    // ...existing imports...
    _ "github.com/brunoga/pipeliner/plugins/filter/my_plugin"
)
```

The `_` import triggers the `init()` function which calls `plugin.Register`. The plugin is then available by name in all config files.

---

## Complete examples

### Minimal filter plugin

```go
// Package minword rejects entries whose title is shorter than min_words words.
package minword

import (
    "fmt"
    "strings"

    "github.com/brunoga/pipeliner/internal/entry"
    "github.com/brunoga/pipeliner/internal/plugin"
    "github.com/brunoga/pipeliner/internal/store"
)

func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "min_words",
        Description: "reject entries whose title has fewer than min_words words",
        PluginPhase: plugin.PhaseFilter,
        Factory:     newPlugin,
        Validate:    validate,
    })
}

func validate(cfg map[string]any) []error {
    return nil // min_words is optional with a default
}

type minWordPlugin struct{ min int }

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
    min := 2
    if v, ok := cfg["min_words"].(float64); ok {
        min = int(v)
    }
    if min < 1 {
        return nil, fmt.Errorf("min_words: must be at least 1")
    }
    return &minWordPlugin{min: min}, nil
}

func (p *minWordPlugin) Name() string        { return "min_words" }
func (p *minWordPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *minWordPlugin) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
    if len(strings.Fields(e.Title)) < p.min {
        e.Reject(fmt.Sprintf("min_words: title has fewer than %d words", p.min))
    }
    return nil
}
```

### Metainfo plugin with persistent cache

```go
package mymeta

import (
    "context"
    "fmt"
    "time"

    "github.com/brunoga/pipeliner/internal/cache"
    "github.com/brunoga/pipeliner/internal/entry"
    "github.com/brunoga/pipeliner/internal/plugin"
    "github.com/brunoga/pipeliner/internal/store"
)

func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "metainfo_myapi",
        Description: "enrich entries with data from MyAPI",
        PluginPhase: plugin.PhaseMetainfo,
        Factory:     newPlugin,
        Validate:    validate,
    })
}

func validate(cfg map[string]any) []error {
    var errs []error
    if err := plugin.RequireString(cfg, "api_key", "metainfo_myapi"); err != nil {
        errs = append(errs, err)
    }
    if err := plugin.OptDuration(cfg, "cache_ttl", "metainfo_myapi"); err != nil {
        errs = append(errs, err)
    }
    return errs
}

type myMetaPlugin struct {
    apiKey string
    cache  *cache.Cache[*MyData]
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
    apiKey, _ := cfg["api_key"].(string)
    if apiKey == "" {
        return nil, fmt.Errorf("metainfo_myapi: 'api_key' is required")
    }
    ttl := 24 * time.Hour
    if v, _ := cfg["cache_ttl"].(string); v != "" {
        d, err := time.ParseDuration(v)
        if err != nil {
            return nil, fmt.Errorf("metainfo_myapi: invalid cache_ttl: %w", err)
        }
        ttl = d
    }
    c := cache.NewPersistent[*MyData](ttl, db.Bucket("cache_metainfo_myapi"))
    c.Preload() // warm the in-memory map at startup
    return &myMetaPlugin{apiKey: apiKey, cache: c}, nil
}

func (p *myMetaPlugin) Name() string        { return "metainfo_myapi" }
func (p *myMetaPlugin) Phase() plugin.Phase { return plugin.PhaseMetainfo }

func (p *myMetaPlugin) Annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
    title := e.Title
    if data, ok := p.cache.Get(title); ok {
        apply(e, data)
        return nil
    }
    data, err := fetchFromAPI(ctx, p.apiKey, title)
    if err != nil {
        tc.Logger.Warn("metainfo_myapi: fetch failed", "title", title, "err", err)
        return nil // don't fail the whole pipeline on a metadata miss
    }
    p.cache.Set(title, data)
    apply(e, data)
    return nil
}

func apply(e *entry.Entry, data *MyData) {
    // Use standard fields where possible; provider-specific fields for the rest.
    e.SetVideoInfo(entry.VideoInfo{
        GenericInfo: entry.GenericInfo{Title: data.Title, Enriched: true},
        Rating:      data.Rating,
        Genres:      data.Genres,
    })
    // Provider-specific fields with no standard equivalent.
    e.Set("myapi_id", data.ID)
}
```

---

## Checklist for new plugins

- [ ] `init()` calls `plugin.Register` with a unique `PluginName`
- [ ] `Factory` validates required fields and returns a descriptive error if missing
- [ ] `Validate` function registered and mirrors factory requirements
- [ ] `Phase()` returns the correct phase constant
- [ ] Bucket names include `tc.Name` to scope data per task
- [ ] Persistent caches call `Preload()` in the constructor
- [ ] Errors from external calls are logged and do not fail the whole pipeline (return nil, log warn)
- [ ] Sub-plugin configs use `MakeFromPlugin` (for `from:` sources) or `ResolveNameAndConfig` (for search plugins)
- [ ] A `README.md` is created in the plugin directory
- [ ] The plugin is added to `cmd/pipeliner/main.go` via a side-effect import
