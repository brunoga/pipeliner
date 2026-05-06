# Plugins

Pipeliner is built entirely from plugins. Each task is a chain of plugins executed in order.

## Phase model

```
input → [metainfo / filter / modify — in config order] → output → learn
```

| Phase | Interface | Purpose |
|-------|-----------|---------|
| **input** | `Input(ctx, tc) ([]*Entry, error)` | Produce entries from a source |
| **metainfo** | `Annotate(ctx, tc, e) error` | Annotate entries with extra fields |
| **filter** | `Filter(ctx, tc, e) error` | Accept, reject, or leave an entry undecided |
| **modify** | `Modify(ctx, tc, e) error` | Mutate entry fields in-place |
| **output** | `Output(ctx, tc, entries) error` | Act on all accepted entries |
| **learn** | `Learn(ctx, tc, entries) error` | Persist state for future runs |

Input and output run as bookend phases (all inputs concurrently, then the processing pipeline, then all outputs concurrently). Metainfo, filter, and modify plugins run **in the order they appear in the config file**, interleaved with each other — a filter can immediately follow the metainfo plugin that sets the field it inspects.

## Filter semantics

Each entry starts **undecided**. Filters can move it to **accepted** or **rejected**. Once rejected, an entry stays rejected (no plugin can un-reject). If an entry is still undecided after all filters run, it is dropped — not passed to output.

## Metainfo and the entry field map

Metainfo plugins annotate accepted entries with extra fields stored in the entry's field map. These fields are then available in Go template expressions in `pathfmt`, `set`, `condition`, `exec`, and output plugins.

Fields are typed (string, int, float64). Template access uses dot notation:

```
{{.series_name}}   {{.trakt_rating}}   {{.tmdb_genres}}
```

## Plugin directories

| Phase | Directory |
|-------|-----------|
| [Input](input/README.md) | `plugins/input/` |
| [Filter](filter/README.md) | `plugins/filter/` |
| [Metainfo](metainfo/README.md) | `plugins/metainfo/` |
| [Modify](modify/README.md) | `plugins/modify/` |
| [Output](output/README.md) | `plugins/output/` |
| [Learn](filter/seen/README.md) | `plugins/filter/` |
| [Notify Notifiers](notify/README.md) | `plugins/notify/` |
