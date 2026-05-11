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
| Produces | `series_season`, `series_episode`, `series_episode_id`, `series_proper`, `series_repack`, `series_double_episode`, `series_service` |
| Requires | — |

## Example

Linear:
```python
task("my-task", [
    plugin("rss", url="https://example.com/feed"),
    plugin("metainfo_series"),
    plugin("pathfmt", path="/media/tv/{title}/Season {series_season:02d}", field="download_path"),
])
```

DAG:
```python
src    = input("rss", url="https://example.com/feed")
meta   = process("metainfo_series", from_=src)
output("print", from_=meta)
pipeline("my-pipeline", schedule="1h")
```
