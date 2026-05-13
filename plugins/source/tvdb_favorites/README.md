# tvdb_favorites

Fetches shows from a TheTVDB user's favorites list and emits one entry per show. Entries carry the show name and a canonical TheTVDB URL, making them suitable as title sources for `discover.list` and `series.list`.

Use as a standalone `input()` source node, or inside `series.list`, `movies.list`, `discover.list`, or `discover.search` config keys.

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
src    = input("rss", url="https://example.com/rss/shows")
seen   = process("seen", upstream=src)
series = process("series", upstream=seen,
    tracking="strict", quality="720p+",
    list=[{"name": "tvdb_favorites", "api_key": "YOUR_TVDB_API_KEY", "user_pin": "YOUR_TVDB_USER_PIN"}])
output("deluge", upstream=series, host="localhost", password="changeme")
pipeline("tv-favorites", schedule="1h")
```

## Notes

- API keys and user PINs are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- On the first run this plugin makes N+1 API calls: one for the favorites ID list and one per show to resolve its name and slug. Results are cached by the calling plugin (`series`, `discover`) according to its TTL setting.

## DAG role

`tvdb_favorites` has `Role=source`. It is used inside `series.list` and `discover.list`, and can also be used as a standalone `input()` node in DAG pipelines:

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `tvdb_id`, `tvdb_year` |
| Requires | — |
