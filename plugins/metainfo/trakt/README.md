# metainfo_trakt

Annotates entries with metadata from Trakt.tv via the search API. Searches by parsed show or movie name and caches results.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `client_id` | string | yes | — | Trakt API Client ID |
| `type` | string | yes | — | `shows` or `movies` |
| `cache_ttl` | string | no | `24h` | How long to cache search results |

## Fields set on entry

### Provider-specific (always)

| Field | Type | Description |
|-------|------|-------------|
| `trakt_id` | int | Trakt ID |
| `trakt_slug` | string | Trakt URL slug |
| `trakt_tmdb_id` | int | TMDb ID |
| `trakt_tvdb_id` | int | TheTVDB ID (shows only) |

### Standard fields (always)

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Title from Trakt |
| `description` | string | Plot summary |
| `enriched` | bool | `true` — Trakt successfully enriched this entry |
| `video_year` | int | Year |
| `video_rating` | float64 | Community rating (0–10) |
| `video_votes` | int | Number of votes |
| `video_imdb_id` | string | IMDb ID |
| `video_genres` | []string | Genre names |

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
    metainfo_trakt:                 # annotate with Trakt metadata
      client_id: YOUR_CLIENT_ID
      type: shows
```

## Notes

- Results are cached in `pipeliner.db` in the same directory as the config file.
- Use `enriched` (not `trakt_id`) to check whether Trakt successfully found metadata: `require: fields: ["enriched"]`.
