# input_trakt

Fetches movies or shows from a Trakt.tv list and emits one entry per item. Entries carry the item title and a canonical Trakt URL, making them suitable as title sources for `discover.from`, `series.from`, and `movies.from`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `client_id` | string | yes | — | Trakt API Client ID |
| `type` | string | yes | — | `movies` or `shows` |
| `list` | string | no | `watchlist` | `watchlist`, `trending`, `popular`, `watched`, `ratings`, `collection` |
| `access_token` | string | conditional | — | OAuth2 bearer token (required for `watchlist`, `ratings`, `collection`) |
| `limit` | int | no | `100` | Max results for public lists |

## Fields set on each entry

| Field | Type | Description |
|-------|------|-------------|
| `trakt_id` | int | Trakt internal ID |
| `trakt_year` | int | Release or premiere year |
| `trakt_imdb_id` | string | IMDb ID (e.g. `tt1375666`) |
| `trakt_tmdb_id` | int | TMDb ID |

## Example — standalone input

```yaml
tasks:
  print-watchlist:
    input_trakt:
      client_id: YOUR_CLIENT_ID
      access_token: YOUR_ACCESS_TOKEN
      type: movies
      list: watchlist
    print:
```

## Example — dynamic title source for series and movies filters

```yaml
tasks:
  tv-watchlist:
    rss:
      url: "https://example.com/rss/shows"
    seen:
    series:
      tracking: strict
      quality: 720p
      from:
        - name: input_trakt
          client_id: YOUR_CLIENT_ID
          access_token: YOUR_ACCESS_TOKEN
          type: shows
          list: watchlist
    transmission:
      host: localhost
```

## Notes

- A free API key is available at [trakt.tv/oauth/applications](https://trakt.tv/oauth/applications).
- `trending`, `popular`, and `watched` are public and require only a `client_id`.
- `watchlist`, `ratings`, and `collection` are private and require an `access_token`.
- The list is re-fetched on every `Run` call. Use inside `series.from` or `movies.from` to benefit from their built-in TTL caching.
