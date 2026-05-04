# input_tvdb

Fetches shows from a TheTVDB user's favorites list and emits one entry per show. Entries carry the show name and a canonical TheTVDB URL, making them suitable as title sources for `discover.from` and `series.from`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `user_pin` | string | yes | — | User PIN from thetvdb.com account settings |

## Fields set on each entry

| Field | Type | Description |
|-------|------|-------------|
| `tvdb_id` | int | TheTVDB series ID |
| `tvdb_year` | string | Premiere year (if known) |

## Example — dynamic title source for the series filter

```yaml
tasks:
  tv-favorites:
    rss:
      url: "https://example.com/rss/shows"
    seen:
    series:
      tracking: strict
      quality: 720p
      from:
        - name: input_tvdb
          api_key: YOUR_TVDB_API_KEY
          user_pin: YOUR_TVDB_USER_PIN
    deluge:
      host: localhost
      password: changeme
```

## Notes

- API keys and user PINs are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- On the first run this plugin makes N+1 API calls: one for the favorites ID list and one per show to resolve its name and slug. Use inside `series.from` to benefit from its built-in TTL caching so the API is not hit on every pipeline run.
