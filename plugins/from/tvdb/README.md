# tvdb_favorites

Fetches shows from a TheTVDB user's favorites list and emits one entry per show. Entries carry the show name and a canonical TheTVDB URL, making them suitable as title sources for `discover.from` and `series.from`.

**This plugin is a PhaseFrom sub-plugin.** It cannot be used directly as a task-level input. Use it inside `series.from`, `movies.from`, or `discover.from`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `user_pin` | string | yes | — | User PIN from thetvdb.com account settings |

## Fields set on each entry

| Field | Type | Description |
|-------|------|-------------|
| `tvdb_id` | string | TheTVDB series ID |
| `tvdb_year` | string | Premiere year (if known) |

## Example — dynamic title source for the series filter

```python
task("tv-favorites", [
    plugin("rss", url="https://example.com/rss/shows"),
    plugin("series",
        tracking="strict",
        quality="720p+",
        **{"from": [
            {"name": "tvdb_favorites", "api_key": "YOUR_TVDB_API_KEY", "user_pin": "YOUR_TVDB_USER_PIN"},
        ]},
    ),
    plugin("deluge", host="localhost", password="changeme"),
])
```

## Notes

- API keys and user PINs are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- On the first run this plugin makes N+1 API calls: one for the favorites ID list and one per show to resolve its name and slug. Results are cached by the calling plugin (`series`, `discover`) according to its TTL setting.

## DAG role

`tvdb_favorites` keeps `PhaseFrom` so it continues to work inside `series.from` and `discover.from`. Its `Role` is `source`, which means it can also be used as a standalone `input()` node in DAG pipelines:

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `tvdb_id` |
| Requires | — |
