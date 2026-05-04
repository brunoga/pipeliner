# trakt

Accepts entries whose parsed title fuzzy-matches something on a Trakt.tv list. Fetches the list once per TTL window and caches the result, so the API is called at most once per TTL regardless of how many entries or how often the process runs.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `client_id` | string | yes | — | Trakt API Client ID |
| `type` | string | yes | — | `shows` or `movies` |
| `list` | string | no | `watchlist` | `watchlist`, `trending`, `popular`, `watched`, `ratings`, `collection` |
| `access_token` | string | conditional | — | OAuth2 bearer token (required for `watchlist`, `ratings`, `collection`) |
| `limit` | int | no | 100 | Max results for public lists |
| `min_rating` | int | no | — | Minimum user rating to include (1–10, `ratings` list only) |
| `ttl` | string | no | `1h` | How long to cache the list |

## Example

```yaml
tasks:
  tv-watchlist:
    rss:
      url: "https://example.com/feed"
    seen:
    trakt:
      client_id: YOUR_CLIENT_ID
      access_token: YOUR_ACCESS_TOKEN
      type: shows
      list: watchlist
    transmission:
      host: localhost
```

## Notes

- The Trakt API key is free. Get one at [trakt.tv/oauth/applications](https://trakt.tv/oauth/applications).
- The `trending` and `popular` lists are public and require only a `client_id`.
- `watchlist`, `ratings`, and `collection` are private and require an `access_token`.
- The cache key includes `type`, `list`, and `min_rating`, so separate plugin instances with different settings coexist safely.
- The cache is stored in `pipeliner.db` in the same directory as the config file.
