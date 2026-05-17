# series

Accepts episodes of configured TV shows. Parses the episode identifier from the entry title, matches the series name with fuzzy matching, and enforces optional quality and ordering constraints.

**Multiple quality variants** of the same episode (from different sources or input feeds) are all accepted; add a `dedup` node after `series` to keep only the best copy.

A re-download of an already-seen episode is accepted when the new copy is strictly better quality, or when it is a PROPER/REPACK that is not a quality downgrade.

The show list can be provided statically via `static`, dynamically via `list` (a list of input plugins whose entry titles are used as show names), or both. Dynamic results are cached for the configured `ttl` so external APIs are not called on every pipeline run.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `static` | string or list | conditional | — | Static show names to accept |
| `list` | list | conditional | — | List-plugin configs whose entry titles supplement the show list |
| `ttl` | string | no | `1h` | How long to cache the dynamic list fetched via `list` |
| `tracking` | string | no | `strict` | Episode ordering mode: `strict`, `backfill`, or `follow` |
| `quality` | string | no | — | Minimum quality spec (e.g. `720p`, `1080p bluray`) |

At least one of `static` or `list` is required.

### Tracking modes

| Mode | Behaviour |
|------|-----------|
| `strict` | Accept only the next expected episode; reject gaps greater than one ahead of the latest downloaded |
| `backfill` | Accept any episode not yet downloaded, including older ones |
| `follow` | On first encounter accept everything (handles full-season binge dumps in one pass); afterwards use the earliest tracked **season** as an anchor — episodes from older seasons are rejected, all episodes in the anchor season or newer are accepted |

#### Choosing a tracking mode

- **`strict`** — weekly airing shows where gaps indicate a missing episode. Does not handle full-season drops well (requires one run per episode).
- **`backfill`** — catching up on a show's entire back-catalogue. Will download all historical episodes that appear in the feed.
- **`follow`** — recommended for new shows and continuing series. Start tracking whenever you first see the show; get entire season drops in one pass; never download episodes from seasons before you started watching. The season is the anchor, so adding the show mid-season still picks up the whole current season.

### `list` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config:

```python
series = process("series", upstream=seen,
    list=[
        {"name": "trakt_list", "client_id": env("TRAKT_ID"),
         "client_secret": env("TRAKT_SECRET"), "type": "shows"},
        {"name": "tvdb_favorites", "api_key": env("TVDB_KEY"), "user_pin": env("TVDB_PIN")},
    ])
```

## Fields set on each entry

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Parsed series name (e.g. `Breaking Bad`) |
| `series_season` | int | Season number |
| `series_episode` | int | Episode number |
| `series_episode_id` | string | Episode identifier (e.g. `S02E05`, `S01E01E02` for doubles) |
| `series_double_episode` | int | Second episode number for double-episode releases |
| `series_proper` | bool | `true` if the release is a PROPER |
| `series_repack` | bool | `true` if the release is a REPACK |
| `series_service` | string | Streaming service tag (e.g. `AMZN`, `NF`) |
| `series_container` | string | Container format tag if present (e.g. `mkv`) |

These are the same fields produced by `metainfo_series`; a separate `metainfo_series` node is not required when `series` is already in the pipeline.

## Example — static list

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",   upstream=src)
series = process("series", upstream=seen, static=["Breaking Bad"],
                 tracking="strict", quality="720p+")
output("transmission", upstream=series, host="localhost")
pipeline("tv", schedule="30m")
```

## Example — dynamic list from Trakt watchlist

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",   upstream=src)
series = process("series", upstream=seen,
    list=[{"name": "trakt_list", "client_id": env("TRAKT_ID"),
           "client_secret": env("TRAKT_SECRET"), "type": "shows"}],
    tracking="follow")
output("transmission", upstream=series, host="localhost")
pipeline("tv-trakt", schedule="30m")
```

## Example — dynamic list from TheTVDB favorites

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",   upstream=src)
series = process("series", upstream=seen,
    list=[{"name": "tvdb_favorites", "api_key": env("TVDB_KEY"), "user_pin": env("TVDB_PIN")}],
    tracking="backfill", quality="720p+")
output("transmission", upstream=series, host="localhost")
pipeline("tv-tvdb", schedule="30m")
```

## Example — combined static and dynamic

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",   upstream=src)
series = process("series", upstream=seen,
    static=["Severance"],     # always included
    list=[{"name": "trakt_list", "client_id": env("TRAKT_ID"),
           "client_secret": env("TRAKT_SECRET"), "type": "shows"}],
    tracking="follow")
output("transmission", upstream=series, host="localhost")
pipeline("tv-combined", schedule="30m")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `title`, `series_season`, `series_episode`, `series_episode_id`, `series_double_episode`, `series_proper`, `series_repack`, `series_service`, `series_container` |
| Requires | — |

## Notes

- Episode history and dynamic list cache are stored in `pipeliner.db` in the same directory as the config file.
- The episode tracker is updated only after all downstream sinks confirm (via `CommitPlugin`). If a sink fails an entry, the episode is not recorded as downloaded and will be retried on the next run.
- **Double episodes** (e.g. `S01E01E02`): when a double-episode release is committed, both individual episodes (`S01E01` and `S01E02`) are also marked as seen, preventing re-download of either part as a standalone release later.
- `metainfo_series` is not needed in pipelines that already include `series` — all the same fields are set by the `series` filter itself.
