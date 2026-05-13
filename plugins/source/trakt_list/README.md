# trakt_list

Fetches movies or shows from a Trakt.tv list and emits one entry per item. Entries carry the item title and a canonical Trakt URL, making them suitable as title sources for `discover.list`, `series.list`, and `movies.list`.

Use as a standalone `input()` source node, or inside `series.list`, `movies.list`, `discover.list`, or `discover.search` config keys.

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

```python
process("movies", upstream=seen,
    list=[{"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
           "client_secret": "YOUR_CLIENT_SECRET", "type": "movies", "list": "watchlist"}])
```

Alternatively, provide a static `access_token` obtained manually from Trakt:

```python
process("movies", upstream=seen,
    list=[{"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
           "access_token": "YOUR_ACCESS_TOKEN", "type": "movies", "list": "watchlist"}])
```

## Fields set on each entry

| Field | Type | Description |
|-------|------|-------------|
| `trakt_id` | int | Trakt internal ID |
| `trakt_year` | int | Release or premiere year |
| `trakt_imdb_id` | string | IMDb ID (e.g. `tt1375666`) |
| `trakt_tmdb_id` | int | TMDb ID |

## Example — dynamic title source for series and movies filters

```python
src    = input("rss", url="https://example.com/rss/shows")
seen   = process("seen", upstream=src)
series = process("series", upstream=seen,
    tracking="strict", quality="720p+",
    list=[{"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
           "client_secret": "YOUR_CLIENT_SECRET", "type": "shows", "list": "watchlist"}])
output("transmission", upstream=series, host="localhost")
pipeline("tv-watchlist", schedule="1h")
```

## Notes

- A free API key is available at [trakt.tv/oauth/applications](https://trakt.tv/oauth/applications). Create an app to get a `client_id` and `client_secret`.
- `trending`, `popular`, and `watched` are public and require only a `client_id`.
- `watchlist`, `ratings`, and `collection` are private and require either `client_secret` (recommended) or `access_token`.
- The list is re-fetched on every `Run` call. Use inside `series.list` or `movies.list` to benefit from their built-in TTL caching.

## DAG role

`trakt_list` has `Role=source`. It is used inside `series.list`, `movies.list`, and `discover.list`, and can also be used as a standalone `input()` node in DAG pipelines:

```python
# DAG: trakt_list as a standalone source feeding a series filter
shows    = input("trakt_list",
    client_id=env("TRAKT_ID"),
    client_secret=env("TRAKT_SECRET"),
    type="shows",
    list="watchlist",
)
src      = input("rss", url="https://example.com/feed")
combined = process("seen", upstream=merge(src, shows))
output("transmission", upstream=combined, host="localhost")
pipeline("trakt-tv", schedule="1h")
```

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `trakt_id`, `trakt_slug`, `trakt_year`, `trakt_imdb_id`, `trakt_tmdb_id` |
| Requires | — |
