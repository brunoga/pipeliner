# Plugins

Pipeliner is built from plugins connected into DAG pipelines. Every plugin
implements one of three interfaces:

## Plugin roles

| Role | Interface | Method | Purpose |
|------|-----------|--------|---------|
| **source** | `SourcePlugin` | `Generate(ctx, tc) ([]*Entry, error)` | Produce entries from an external source |
| **processor** | `ProcessorPlugin` | `Process(ctx, tc, entries) ([]*Entry, error)` | Transform entries: filter, enrich, or modify |
| **sink** | `SinkPlugin` | `Consume(ctx, tc, entries) error` | Act on accepted entries (download, notify, persist) |

## Entry state

Each entry starts **undecided**. Processors can move it to **accepted** or **rejected**
via `e.Accept()` and `e.Reject(reason)`. Sinks receive only `Accepted` entries
(enforced by `entry.FilterAccepted`). Rejected entries are counted and reported
but do not reach sinks.

Processors that drop entries from their output slice should call `e.Reject(reason)`
first so the rejection is counted correctly in the result.

## Field map

Entries carry an arbitrary `Fields map[string]any` that processors read and write.
Standard field prefixes: `video_`, `series_`, `movie_`, `torrent_`, `file_`, `rss_`.
See `internal/entry/info.go` for the full list of constants.

## Plugin directories

| Role | Directory |
|------|-----------|
| Sources | `plugins/input/`, `plugins/from/` |
| Processors — enrichment | `plugins/metainfo/` |
| Processors — filtering | `plugins/filter/` |
| Processors — field mutation | `plugins/modify/` |
| Sinks | `plugins/output/` |
| Sink notifiers (used by `notify`) | `plugins/notify/` |

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
