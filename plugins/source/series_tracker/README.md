# series_tracker

Emits one entry per show tracked by the series tracker — the same persistent store the [`series`](../../processor/filter/series/README.md) and [`premiere`](../../processor/filter/premiere/README.md) filters write per-episode download records to.

The tracker bucket is shared across **all** tasks (it is not namespaced by task name), so this source sees every show tracked by any pipeline with no extra configuration. Feed it into [`series_lifecycle`](../../processor/metainfo/lifecycle/README.md) to classify shows as complete/dormant/active, or use it as a `list=` source for `series`/`discover`.

## Config

No config keys.

## Fields set on entry

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Display name from the most recent tracker record, falling back to the normalized name |
| `series_name` | string | Normalized show name — the tracker key |
| `media_type` | string | Always `series` |
| `source` | string | Always `series_tracker:tracker` |
| `series_episode_count` | int | Number of downloaded episodes on record |
| `series_newest_episode` | string | Highest tracked episode ID (e.g. `S03E08`) |
| `series_last_downloaded_at` | time | Most recent download timestamp (unset only for legacy records without one) |
| `series_inactive` | bool | `true` when the show has been deactivated via [`series_tracker_update`](../../sink/series_tracker_update/README.md) |

The entry URL is the synthetic, stable `pipeliner://series/<normalized-name>` so `dedup` and cross-branch matching by URL work.

## Example

```python
shows = input("series_tracker")
lc    = process("series_lifecycle", upstream=shows, api_key=tvdb_api_key)
```

See `configs/series-lifecycle.star` for the full lifecycle pipeline.

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `source`, `media_type`, `series_name`, `series_episode_count`, `series_newest_episode`, `series_inactive` |
| MayProduce | `series_last_downloaded_at` |
| List plugin | yes (usable inside `list=[…]`) |
