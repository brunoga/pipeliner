# premiere

Accepts only the first episode of previously unseen series. Useful for
discovering new shows: once a series premiere is accepted, subsequent episodes
of that show are rejected by this filter (other filters such as `series` take
over).

State is persisted in SQLite so the filter is effective across runs.

## Config

```yaml
premiere:
  episode: 1        # episode number to treat as premiere (default: 1)
  season: 1         # season number; set to 0 to accept any season (default: 1)
  db: premiere.db   # SQLite database path (default: pipeliner.db)
```

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
      db: premiere.db
    deluge:
      path: /downloads/tv
```
