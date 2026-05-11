# tvdb

Accepts series entries whose parsed show name fuzzy-matches a show in the user's TheTVDB favorites list. Fetches and caches the favorites list; a fresh process restart reuses the cache until the TTL expires.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB API key |
| `user_pin` | string | yes | — | User PIN from thetvdb.com account settings |
| `ttl` | string | no | `1h` | How long to cache the favorites list |
| `reject_unmatched` | bool | no | `true` | Reject entries that do not match the favorites list; set `false` to leave them undecided when chaining filters |

## Example

```python
task("tv-favorites", [
    plugin("rss", url="https://example.com/feed"),
    plugin("seen"),
    plugin("tvdb", api_key="YOUR_API_KEY", user_pin="YOUR_USER_PIN"),
    plugin("deluge", host="localhost", password="changeme"),
])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |

## Notes

- API keys and user PINs are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- Resolving favorites requires N+1 API calls on first fetch (one for the ID list, one per series to get its name). Subsequent calls within the TTL window hit only the local cache.
- The cache is stored in `pipeliner.db` in the same directory as the config file.
