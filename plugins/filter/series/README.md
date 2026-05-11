# series

Accepts episodes of configured TV shows. Parses the episode identifier from the entry title, matches the series name with fuzzy matching, and enforces optional quality and ordering constraints.

**Multiple quality variants** of the same episode (from different sources or input feeds) are all accepted so the task engine's automatic deduplication can pick the best copy. The download history is updated in the Learn phase — only the winning copy is recorded.

A re-download of an already-seen episode is accepted when the new copy is strictly better quality, or when it is a PROPER/REPACK that is not a quality downgrade.

The show list can be provided statically via `static`, dynamically via `from` (a list of input plugins whose entry titles are used as show names), or both. Dynamic results are cached for the configured `ttl` so external APIs are not called on every pipeline run.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `static` | string or list | conditional | — | Static show names to accept |
| `from` | list | conditional | — | Input plugin configs whose entry titles supplement the show list |
| `ttl` | string | no | `1h` | How long to cache the dynamic list fetched via `from` |
| `tracking` | string | no | `strict` | Episode ordering mode: `strict`, `backfill`, or `follow` |
| `quality` | string | no | — | Minimum quality spec (e.g. `720p`, `1080p bluray`) |

At least one of `static` or `from` is required.

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

### `from` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config:

```python
plugin("series", **{"from": [
    "trakt_list",                  # name only, uses defaults
    {"name": "tvdb_favorites", "api_key": "YOUR_KEY", "user_pin": "YOUR_PIN"},
]})
```

## Fields set on each entry

| Field | Type | Description |
|-------|------|-------------|
| `series_season` | int | Season number |
| `series_episode` | int | Episode number |
| `series_episode_id` | string | Episode identifier string (e.g. `S02E05`) |

## Example — static list

```python
task("tv", [
    plugin("rss", url="https://example.com/feed"),
    plugin("seen"),
    plugin("series",
        tracking="strict",
        quality="720p",
        static=["Breaking Bad", "Better Call Saul", "The Wire"],
    ),
])
```

## Example — dynamic list from Trakt watchlist

```python
task("tv-watchlist", [
    plugin("rss", url="https://example.com/feed"),
    plugin("seen"),
    plugin("series",
        tracking="strict",
        quality="720p",
        ttl="2h",
        **{"from": [
            {"name": "trakt_list", "client_id": "YOUR_TRAKT_CLIENT_ID",
             "access_token": "YOUR_TRAKT_ACCESS_TOKEN", "type": "shows", "list": "watchlist"},
        ]},
    ),
])
```

## Example — dynamic list from TheTVDB favorites

```python
task("tv-favorites", [
    plugin("rss", url="https://example.com/feed"),
    plugin("seen"),
    plugin("series",
        tracking="strict",
        quality="720p",
        **{"from": [
            {"name": "tvdb_favorites", "api_key": "YOUR_TVDB_API_KEY", "user_pin": "YOUR_TVDB_USER_PIN"},
        ]},
    ),
])
```

## Example — combined static and dynamic

```python
task("tv-combined", [
    plugin("rss", url="https://example.com/feed"),
    plugin("series",
        static=["Severance"],      # always included regardless of watchlist
        **{"from": [
            {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
             "access_token": "YOUR_ACCESS_TOKEN", "type": "shows", "list": "watchlist"},
        ]},
    ),
])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `series_season`, `series_episode`, `series_episode_id` |
| Requires | — |

## Notes

- Episode history and dynamic list cache are stored in `pipeliner.db` in the same directory as the config file.
