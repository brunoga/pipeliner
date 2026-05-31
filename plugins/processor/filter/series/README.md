# series

Accepts episodes of TV shows. Optionally matches the series name against a configured list (fuzzy match), enforces optional quality and ordering constraints, and persists download history across runs.

**Multiple quality variants** of the same episode (from different sources or input feeds) are all accepted; add a `dedup` node after `series` to keep only the best copy.

A re-download of an already-seen episode is accepted when the new copy is strictly better quality, or when it is a PROPER/REPACK that is not a quality downgrade.

The show list is optional. When provided, it can be static via `static`, dynamic via `list` (input plugins whose entry titles are used as show names), or both — dynamic results are cached for the configured `ttl`. **With neither set, the filter operates in accept-all mode**: every classified episode passes the upstream requirements and the quality/tracker checks. The tracker still dedups and detects upgrades by series name + episode ID, so a no-list config is the right choice for "download every X-quality episode I find" pipelines.

## Upstream requirement

`series` reads episode metadata from entry fields; it does **not** parse the title itself. You must run [`metainfo_file`](../../metainfo/file/README.md) (or any equivalent plugin that sets the same fields) upstream:

| Field | Used for |
|-------|----------|
| `title` | Show name (normalized) used for matching against the configured list |
| `series_episode_id` | Tracker key + classification gate |
| `series_season` | `follow` season-floor logic |
| `series_episode` | Persist + double-episode part marking |
| `_quality` *(via `e.Quality()`)* | Quality spec matching, upgrade comparison, persisted record |
| `series_double_episode` *(optional)* | Marks each part of a double episode on commit |
| `video_proper`, `video_repack` *(optional)* | PROPER/REPACK upgrade detection |

The first five fields are declared via `Descriptor.Requires`, so the DAG validator catches pipelines that wire `series` without an upstream metainfo step.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `static` | no | — | Optional static show names to accept; omit (alongside `list`) for accept-all mode |
| `list` | no | — | Optional list-plugin configs whose entry titles supplement the show list; omit (alongside `static`) for accept-all mode |
| `ttl` | no | `1h` | How long to cache the dynamic list fetched via `list` |
| `tracking` | no | `strict` | Episode ordering mode: `strict`, `backfill`, or `follow` |
| `quality` | no | — | Quality spec (e.g. `720p+` for floor, `720p` for exact, `720p-1080p` for range) |
| `reject_unmatched` | no | `true` | Reject entries that lack `series_episode_id` (i.e. were not classified as a series episode upstream). When a list is configured, also reject entries whose show name isn't in the list. With neither `static` nor `list` set, this flag only governs the classification check. |

Both `static` and `list` are optional. With neither set the filter accepts every classified episode that passes the quality spec and tracker checks.

### Tracking modes

| Mode | Behaviour |
|------|-----------|
| `strict` | Accept only the next expected episode; reject gaps greater than one ahead of the latest downloaded |
| `backfill` | Accept any episode not yet downloaded, including older ones |
| `follow` | On first encounter accept everything (handles full-season binge dumps in one pass); afterwards the highest tracked episode defines the season floor — episodes from seasons older than the current position are rejected, all episodes in the current season or newer are accepted (including gap-fills) |

#### Choosing a tracking mode

- **`strict`** — weekly airing shows where gaps indicate a missing episode. Does not handle full-season drops well (requires one run per episode).
- **`backfill`** — catching up on a show's entire back-catalogue. Will download all historical episodes that appear in the feed.
- **`follow`** — recommended for new shows and continuing series. On first encounter the entire season drop is accepted in one pass. Afterwards episodes from seasons older than the current tracking position are ignored — once you are at S05, S01 will not be re-downloaded. Gap-fills within the current season are still picked up in later runs.

### `list` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config:

```python
series = process("series", upstream=meta,
    list=[
        {"name": "trakt_list", "client_id": env("TRAKT_ID"),
         "client_secret": env("TRAKT_SECRET"), "type": "shows"},
        {"name": "tvdb_favorites", "api_key": env("TVDB_KEY"), "user_pin": env("TVDB_PIN")},
    ])
```

## Example — static list

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)   # sets series_*, _quality, etc.
series = process("series",        upstream=meta, static=["Breaking Bad"],
                 tracking="strict", quality="720p+")
output("transmission", upstream=series, host="localhost")
pipeline("tv", schedule="30m")
```

## Example — dynamic list from Trakt watchlist

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)
series = process("series",        upstream=meta,
    list=[{"name": "trakt_list", "client_id": env("TRAKT_ID"),
           "client_secret": env("TRAKT_SECRET"), "type": "shows"}],
    tracking="follow")
output("transmission", upstream=series, host="localhost")
pipeline("tv-trakt", schedule="30m")
```

## Example — dynamic list from TheTVDB favorites

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)
series = process("series",        upstream=meta,
    list=[{"name": "tvdb_favorites", "api_key": env("TVDB_KEY"), "user_pin": env("TVDB_PIN")}],
    tracking="backfill", quality="720p+")
output("transmission", upstream=series, host="localhost")
pipeline("tv-tvdb", schedule="30m")
```

## Example — combined static and dynamic

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)
series = process("series",        upstream=meta,
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
| Requires | `title`, `series_episode_id`, `series_season`, `series_episode`, `_quality` (declared via `RequireAll`) |
| Produces | `media_type` (= `"series"`) |

`series` is a classifier by construction — every entry it lets through is a series episode — so it stamps `media_type = "series"` on each processed entry. Downstream nodes (e.g. `dedup`, `route`, `condition`) can rely on the field being present as Certain, instead of inheriting `metainfo_file`'s conditional `MayProduce`. The `series_*` fields are available downstream because `metainfo_file` (upstream) already set them.

## Notes

- Episode history and dynamic list cache are stored in `pipeliner.db` in the same directory as the config file.
- The episode tracker is updated only after all downstream sinks confirm (via `CommitPlugin`). If a sink fails an entry, the episode is not recorded as downloaded and will be retried on the next run.
- **Double episodes** (e.g. `S01E01E02`): when a double-episode release is committed, both individual episodes (`S01E01` and `S01E02`) are also marked as seen, preventing re-download of either part as a standalone release later.
