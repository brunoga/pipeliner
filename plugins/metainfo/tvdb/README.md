# metainfo_tvdb

Enriches series entries with metadata from TheTVDB. Searches by parsed series name and caches results. If a specific season and episode are parsed, also fetches episode-level detail.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `cache_ttl` | string | no | `24h` | How long to cache search results |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `tvdb_id` | TheTVDB series ID |
| `tvdb_series_name` | Series name from TheTVDB |
| `tvdb_series_year` | Premiere year |
| `tvdb_overview` | Series overview |
| `tvdb_slug` | URL slug |
| `tvdb_episode_id` | TheTVDB episode ID (if episode parsed) |
| `tvdb_episode_name` | Episode title (if episode parsed) |
| `tvdb_air_date` | Original air date (if episode parsed) |
| `tvdb_episode_overview` | Episode overview (if episode parsed) |

## Example

```yaml
tasks:
  tv:
    rss:
      url: "https://example.com/feed"
    series:
      shows: ["Breaking Bad"]
    metainfo_tvdb:
      api_key: YOUR_API_KEY
```

## Notes

- API keys at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- Only annotates entries whose title can be parsed as a series episode. Non-episode titles are skipped.
- Results are cached in `pipeliner.db` in the same directory as the config file.
