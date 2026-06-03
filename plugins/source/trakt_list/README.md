# trakt_list

Fetches movies or shows from a Trakt.tv list and emits one entry per item. Entries carry the item title and a canonical Trakt URL, making them suitable as upstream nodes feeding `discover`, or as title sources inside `series.list` / `movies.list`.

Use as a standalone `input()` source node, or inside `series.list`, `movies.list`, or `discover.search` config keys.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `client_id` | yes | — | Trakt API Client ID |
| `type` | yes | — | `movies` or `shows` |
| `list` | no | `watchlist` | `watchlist`, `trending`, `popular`, `watched`, `ratings`, `collection` |
| `client_secret` | conditional | — | OAuth client secret — required for `watchlist`, `ratings`, and `collection`. Enables automatic token management. |
| `access_token` | conditional | — | Static OAuth bearer token (alternative to `client_secret`) |
| `limit` | no | `100` | Max results for public lists |

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

Trakt list responses use `extended=full` and return rating, votes, genres, and overview alongside titles and IDs — so `trakt_list` populates the standard `video_*` fields directly. A downstream `metainfo_trakt` step is only needed when you also want a fresh provider lookup (e.g. for entries that didn't start out as a Trakt list item).

| Field | Description |
|-------|-------------|
| `source` | Origin in the form `trakt_list:<list>` (e.g. `trakt_list:watchlist`) |
| `enriched` | `true` — set so downstream metainfo nodes know the entry already carries provider data |
| `title` | Canonical title from Trakt |
| `media_type` | `"series"` (when `type=shows`) or `"movie"` (when `type=movies`) |
| `description` | Trakt overview |
| `video_year` | Release or premiere year |
| `video_rating` | Trakt user rating, 0-10 |
| `video_votes` | Vote count behind `video_rating` |
| `video_genres` | List of Trakt genres |
| `video_imdb_id` | IMDb ID (also exposed as `trakt_imdb_id` for back-compat) |
| `trakt_id` | Trakt internal ID |
| `trakt_imdb_id` | IMDb ID (e.g. `tt1375666`) |
| `trakt_tmdb_id` | TMDb ID |

## Example — dynamic title source for series and movies filters

```python
src    = input("rss", url="https://example.com/rss/shows")
seen   = process("seen", upstream=src)
meta   = process("metainfo_file", upstream=seen)
series = process("series", upstream=meta,
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

`trakt_list` has `Role=source`. It is used inside `series.list` and `movies.list`, and can also be used as a standalone `input()` node feeding `discover` or any other DAG pipeline:

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
| Produces | `title`, `media_type` (= `"series"` when `type=shows`, `"movie"` when `type=movies`), `source` |
| MayProduce | `enriched`, `description`, `video_year`, `video_rating`, `video_votes`, `video_genres`, `video_imdb_id`, `trakt_id`, `trakt_imdb_id`, `trakt_tmdb_id` |
| Requires | — |
