# tvdb

Accepts series entries whose parsed show name fuzzy-matches a show in the user's TheTVDB favorites list. Fetches and caches the favorites list in SQLite; a fresh process restart reuses the cache until the TTL expires.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB API key |
| `user_pin` | string | yes | — | User PIN from thetvdb.com account settings |
| `ttl` | string | no | `1h` | How long to cache the favorites list |
| `db` | string | no | `pipeliner.db` | SQLite path for persistent cache |

## Example

```yaml
tasks:
  tv-favorites:
    rss:
      url: "https://example.com/feed"
    seen:
      db: pipeliner.db
    tvdb:
      api_key: YOUR_API_KEY
      user_pin: YOUR_USER_PIN
      db: pipeliner.db
    deluge:
      host: localhost
      password: changeme
```

## Notes

- API keys and user PINs are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- Resolving favorites requires N+1 API calls on first fetch (one for the ID list, one per series to get its name). Subsequent calls within the TTL window hit only the local cache.
