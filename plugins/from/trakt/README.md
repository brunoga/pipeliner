# trakt_list

Fetches movies or shows from a Trakt.tv list and emits one entry per item. Entries carry the item title and a canonical Trakt URL, making them suitable as title sources for `discover.from`, `series.from`, and `movies.from`.

**This plugin is a PhaseFrom sub-plugin.** It cannot be used directly as a task-level input. Use it inside `series.from`, `movies.from`, or `discover.from`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `client_id` | string | yes | — | Trakt API Client ID |
| `type` | string | yes | — | `movies` or `shows` |
| `list` | string | no | `watchlist` | `watchlist`, `trending`, `popular`, `watched`, `ratings`, `collection` |
| `client_secret` | string | conditional | — | OAuth client secret; enables automatic token management via `pipeliner.db`. Run `pipeliner auth trakt` once to authorise. |
| `access_token` | string | conditional | — | Static OAuth bearer token (alternative to `client_secret`) |
| `limit` | int | no | `100` | Max results for public lists |

One of `client_secret` or `access_token` is required for `watchlist`, `ratings`, and `collection`. Public lists (`trending`, `popular`, `watched`) need neither.

## Authentication

The recommended approach is `client_secret` with managed tokens:

```
pipeliner auth trakt --client-id=YOUR_ID --client-secret=YOUR_SECRET
```

This runs the Trakt device auth flow interactively and stores the token in `pipeliner.db`. The token is refreshed automatically before expiry on subsequent runs. Then in your config:

```yaml
from:
  - name: trakt_list
    client_id: YOUR_CLIENT_ID
    client_secret: YOUR_CLIENT_SECRET
    type: movies
    list: watchlist
```

Alternatively, provide a static `access_token` obtained manually from Trakt:

```yaml
from:
  - name: trakt_list
    client_id: YOUR_CLIENT_ID
    access_token: YOUR_ACCESS_TOKEN
    type: movies
    list: watchlist
```

## Fields set on each entry

| Field | Type | Description |
|-------|------|-------------|
| `trakt_id` | int | Trakt internal ID |
| `trakt_year` | int | Release or premiere year |
| `trakt_imdb_id` | string | IMDb ID (e.g. `tt1375666`) |
| `trakt_tmdb_id` | int | TMDb ID |

## Example — dynamic title source for series and movies filters

```yaml
tasks:
  tv-watchlist:
    rss:
      url: "https://example.com/rss/shows"
    series:
      tracking: strict
      quality: 720p+
      from:
        - name: trakt_list
          client_id: YOUR_CLIENT_ID
          client_secret: YOUR_CLIENT_SECRET
          type: shows
          list: watchlist
    transmission:
      host: localhost
```

## Notes

- A free API key is available at [trakt.tv/oauth/applications](https://trakt.tv/oauth/applications). Create an app to get a `client_id` and `client_secret`.
- `trending`, `popular`, and `watched` are public and require only a `client_id`.
- `watchlist`, `ratings`, and `collection` are private and require either `client_secret` (recommended) or `access_token`.
- The list is re-fetched on every `Run` call. Use inside `series.from` or `movies.from` to benefit from their built-in TTL caching.
