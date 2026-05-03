# metainfo_trakt

Annotates entries with metadata from Trakt.tv via the search API. Searches by parsed show or movie name, caches results in SQLite.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `client_id` | string | yes | — | Trakt API Client ID |
| `type` | string | yes | — | `shows` or `movies` |
| `cache_ttl` | string | no | `24h` | How long to cache search results |
| `db` | string | no | `pipeliner.db` | SQLite path for persistent cache |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `trakt_id` | Trakt ID |
| `trakt_slug` | Trakt URL slug |
| `trakt_imdb_id` | IMDb ID |
| `trakt_tmdb_id` | TMDb ID |
| `trakt_tvdb_id` | TheTVDB ID (shows only) |
| `trakt_title` | Title from Trakt |
| `trakt_year` | Year |
| `trakt_overview` | Plot summary |
| `trakt_rating` | Community rating (0–10) |
| `trakt_votes` | Number of votes |
| `trakt_genres` | Comma-separated genre names |

## Example

```yaml
tasks:
  tv:
    rss:
      url: "https://example.com/feed"
    trakt:                          # filter: accept watchlist shows
      client_id: YOUR_CLIENT_ID
      access_token: YOUR_TOKEN
      type: shows
      list: watchlist
      db: pipeliner.db
    metainfo_trakt:                 # annotate with Trakt metadata
      client_id: YOUR_CLIENT_ID
      type: shows
      db: pipeliner.db
```
