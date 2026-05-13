# Plugins

Pipeliner is built from plugins connected into pipelines. Every plugin implements one of three roles:

## Plugin roles

| Role | Interface | Method | Purpose |
|------|-----------|--------|---------|
| **source** | `SourcePlugin` | `Generate(ctx, tc) ([]*Entry, error)` | Produce entries from an external source |
| **processor** | `ProcessorPlugin` | `Process(ctx, tc, entries) ([]*Entry, error)` | Transform entries: filter, enrich, or modify |
| **sink** | `SinkPlugin` | `Consume(ctx, tc, entries) error` | Act on accepted entries (download, notify, persist) |

## Directory layout

```
plugins/
  source/          sources (RSS, files, indexers, Trakt, TVDB)
  processor/
    metainfo/      enrichment processors (quality, series, TMDb, TVDB, Trakt, torrent)
    filter/        accept/reject processors (series, movies, seen, quality, condition…)
    modify/        field-mutation processors (pathfmt, set)
    discover/      active-search processor
  sink/
    notify/        notify sub-plugins (email, pushover, webhook)
    transmission/  torrent client sinks
    deluge/
    qbittorrent/
    download/
    email/
    exec/
    decompress/
    list_add/
    print/
```

## Entry state

Each entry starts **undecided**. Processors move it to **accepted** or **rejected** via
`e.Accept()` and `e.Reject(reason)`. Sinks receive only `Accepted` entries
(enforced by `entry.FilterAccepted`).

## Field map

Entries carry an arbitrary `Fields map[string]any` that processors read and write.
Standard field prefixes: `video_`, `series_`, `movie_`, `torrent_`, `file_`, `rss_`.
See `internal/entry/info.go` for the full list of constants.

## Registering a plugin

```go
func init() {
    plugin.Register(&plugin.Descriptor{
        PluginName:  "my_plugin",
        Description: "one-line description",
        Role:        plugin.RoleProcessor,
        Produces:    []string{"my_field"},
        Requires:    []string{"series_episode_id"},
        Factory:     newPlugin,
        Validate:    validate, // optional
        Schema:      []plugin.FieldSchema{...}, // optional, enables visual editor
    })
}
```

Blank-import the package in `cmd/pipeliner/main.go` to register it.
