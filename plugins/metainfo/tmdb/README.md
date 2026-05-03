# metainfo_tmdb

Enriches movie entries with metadata from The Movie Database (TMDb). Searches by parsed title and year, caches results in SQLite.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TMDb API v3 key |
| `cache_ttl` | string | no | `24h` | How long to cache search results |
| `db` | string | no | `pipeliner.db` | SQLite path for persistent cache |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `tmdb_id` | TMDb movie ID |
| `tmdb_title` | Movie title from TMDb |
| `tmdb_original_title` | Original language title |
| `tmdb_release_date` | Release date (`YYYY-MM-DD`) |
| `tmdb_overview` | Plot summary |
| `tmdb_popularity` | TMDb popularity score |
| `tmdb_vote_average` | Average user rating |
| `tmdb_runtime` | Runtime in minutes |
| `tmdb_tagline` | Tagline |
| `tmdb_imdb_id` | IMDb ID (e.g. `tt1375666`) |
| `tmdb_genres` | Comma-separated genre names |

## Example

```yaml
tasks:
  movies:
    rss:
      url: "https://example.com/feed"
    movies:
      movies: ["Inception"]
      db: pipeliner.db
    metainfo_tmdb:
      api_key: YOUR_API_KEY
      db: pipeliner.db
    pathfmt:
      path: "/media/movies/{{.tmdb_title}} ({{.tmdb_release_date | slice 0 4}})"
```

## Notes

- Free API keys at [themoviedb.org/settings/api](https://www.themoviedb.org/settings/api).
- Only annotates entries whose title can be parsed as a movie (title + year). Entries without a parseable year are skipped.
