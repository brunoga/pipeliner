# metainfo_series

Parses series and episode information from the entry title. Takes no config.

## Fields set on entry

| Field | Type | Example | Description |
|-------|------|---------|-------------|
| `title` | string | `Breaking Bad` | Parsed series name |
| `series_season` | int | `1` | Season number |
| `series_episode` | int | `1` | Episode number |
| `series_episode_id` | string | `S01E01` | Episode identifier string |
| `series_proper` | bool | `true` | Whether the release is a PROPER |
| `series_repack` | bool | `false` | Whether the release is a REPACK |
| `series_double_episode` | int | `2` | Second episode number for double-episode releases (if present) |
| `series_service` | string | `AMZN` | Streaming service tag (if present) |
| `series_container` | string | `mkv` | Container format tag (if present) |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `title`, `series_season`, `series_episode`, `series_episode_id`, `series_proper`, `series_repack`, `series_double_episode`, `series_service`, `series_container` |
| Requires | — |

## Example

```python
src    = input("rss", url="https://example.com/feed")
meta   = process("metainfo_series", upstream=src)
output("print", upstream=meta)
pipeline("my-pipeline", schedule="1h")
```
