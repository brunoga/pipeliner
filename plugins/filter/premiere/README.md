# premiere

Accepts only the first episode of previously unseen series. Useful for
discovering new shows: once a series premiere is accepted, subsequent episodes
of that show are rejected by this filter (other filters such as `series` take
over).

State is persisted in `pipeliner.db` in the same directory as the config file.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `episode` | int | no | `1` | Episode number to treat as premiere |
| `season` | int | no | `1` | Season number to match; `0` means any season |

Requires `series_name`, `series_season`, and `series_episode` fields to be set
(e.g., by `metainfo_series` or the `series` filter). Entries without
`series_name` are rejected.

## Example

```yaml
tasks:
  discover-shows:
    rss:
      url: "https://example.com/rss"
    metainfo_series:
    metainfo_quality:
    quality:
      min: 720p
    premiere:
    deluge:
      path: /downloads/tv
```
