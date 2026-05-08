# trakt

Accepts entries whose parsed title fuzzy-matches something on a Trakt.tv list. Fetches the list once per TTL window and caches the result, so the API is called at most once per TTL regardless of how many entries or how often the process runs.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `client_id` | string | yes | — | Trakt API Client ID |
| `type` | string | yes | — | `shows` or `movies` |
| `list` | string | no | `watchlist` | `watchlist`, `trending`, `popular`, `watched`, `ratings`, `collection` |
| `client_secret` | string | conditional | — | OAuth client secret; enables automatic token management via `pipeliner.db`. Run `pipeliner auth trakt` once to authorise. |
| `access_token` | string | conditional | — | Static OAuth bearer token (alternative to `client_secret`) |
| `limit` | int | no | 100 | Max results for public lists |
| `min_rating` | int | no | — | Minimum user rating to include (1–10, `ratings` list only) |
| `ttl` | string | no | `1h` | How long to cache the list |
| `reject_unmatched` | bool | no | `true` | Reject entries that do not match the list; set `false` to leave them undecided when chaining filters |

One of `client_secret` or `access_token` is required for `watchlist`, `ratings`, and `collection`.

## Authentication

The recommended approach is `client_secret` with managed tokens:

```
pipeliner auth trakt --client-id=YOUR_ID --client-secret=YOUR_SECRET
```

This runs the Trakt device auth flow interactively and stores the token in `pipeliner.db`. The token is refreshed automatically before expiry. Then in your config:

```yaml
trakt:
  client_id: YOUR_CLIENT_ID
  client_secret: YOUR_CLIENT_SECRET
  type: shows
  list: watchlist
```

## Example

```yaml
tasks:
  tv-watchlist:
    rss:
      url: "https://example.com/feed"
    trakt:
      client_id: YOUR_CLIENT_ID
      client_secret: YOUR_CLIENT_SECRET
      type: shows
      list: watchlist
    transmission:
      host: localhost
```

## Notes

- A free API key is available at [trakt.tv/oauth/applications](https://trakt.tv/oauth/applications). Create an app to get a `client_id` and `client_secret`.
- `trending` and `popular` lists are public and require only a `client_id`.
- `watchlist`, `ratings`, and `collection` are private and require either `client_secret` (recommended) or `access_token`.
- The cache key includes `type`, `list`, and `min_rating`, so separate plugin instances with different settings coexist safely.
- The cache is stored in `pipeliner.db` in the same directory as the config file.
