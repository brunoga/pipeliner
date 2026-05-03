# metainfo_series

Parses series and episode information from the entry title. Takes no config.

## Fields set on entry

| Field | Example | Description |
|-------|---------|-------------|
| `series_name` | `Breaking Bad` | Parsed series name |
| `series_season` | `1` | Season number |
| `series_episode` | `1` | Episode number |
| `series_episode_id` | `S01E01` | Episode identifier string |
| `series_proper` | `true` | Whether the release is a PROPER |
| `series_repack` | `false` | Whether the release is a REPACK |
| `series_service` | `AMZN` | Streaming service tag (if present) |

## Example

```yaml
tasks:
  my-task:
    rss:
      url: "https://example.com/feed"
    metainfo_series:
    pathfmt:
      path: "/media/tv/{{.series_name}}/Season {{printf \"%02d\" .series_season}}"
```
