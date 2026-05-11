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
src = input("rss", url="https://example.com/rss")
flt = process("tvdb", from_=src,
              api_key=env("TVDB_KEY"), user_pin=env("TVDB_PIN"))
output("transmission", from_=flt, host="localhost")
pipeline("tvdb-filtered")
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
