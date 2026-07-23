# series_lifecycle

Classifies tracked shows by follow lifecycle using TheTVDB status and episode list, compared against the series tracker:

| `series_lifecycle` | Meaning |
|--------------------|---------|
| `complete` | Status `Ended`/`Cancelled` **and** every aired episode is in the tracker |
| `dormant` | Status `Ended`/`Cancelled` but aired episodes are missing — a backfill candidate |
| `active` | Anything else: still running, upcoming, **or the lookup failed** (missing data never deactivates a show) |

Typically fed by the [`series_tracker`](../../../source/series_tracker/README.md) source and routed into [`series_tracker_update`](../../../sink/series_tracker_update/README.md) / `notify` sinks. See `configs/series-lifecycle.star`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `cache_ttl` | string | no | `24h` | How long to cache TVDB lookups |
| `include_specials` | bool | no | `false` | Count season-0 specials as aired episodes |

## Behavior

- Upstream entries need `series_name` (the normalized tracker key) or at least a `title` (normalized on the fly).
- The show is resolved on TVDB by `tvdb_id` when the entry carries one, otherwise by name search. Lookups are cached in `pipeliner.db` (`cache_series_lifecycle`, `cache_series_lifecycle_eps`).
- Only episodes whose air date is **in the past** count as aired; unaired and undated episodes are ignored. Specials (season 0) are excluded unless `include_specials=true`.
- If the show is ended but the episode list cannot be fetched, it classifies as `active` — never `complete` (which would deactivate it) or `dormant` (which would trigger backfill flows) on unverified data.
- Date-numbered shows (tracker IDs like `2023-11-15`) cannot be matched against TVDB's season/episode numbering, so an ended date-numbered show classifies as `dormant` rather than `complete`.

## Fields set on entry

| Field | Type | When | Description |
|-------|------|------|-------------|
| `series_lifecycle` | string | always | `complete`, `dormant`, or `active` |
| `series_status` | string | lookup succeeded | Verbatim TVDB status (`Continuing`, `Ended`, `Cancelled`, `Upcoming`) |
| `tvdb_id` | string | lookup succeeded | TheTVDB series ID |
| `series_aired_episode_count` | int | episode list fetched | Episodes that have already aired |
| `series_missing_episode_count` | int | episode list fetched | Aired episodes absent from the tracker |

## Example

```python
shows  = input("series_tracker")
lc     = process("series_lifecycle", upstream=shows, api_key=tvdb_api_key)
routes = route(lc,
    complete = 'series_lifecycle == "complete"',
    dormant  = 'series_lifecycle == "dormant"',
    active   = 'series_lifecycle == "active"')
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `series_lifecycle` |
| MayProduce | `series_status`, `series_aired_episode_count`, `series_missing_episode_count`, `tvdb_id` |
| Requires | `series_name` OR `title` |
