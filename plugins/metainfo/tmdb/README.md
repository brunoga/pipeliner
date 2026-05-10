# metainfo_tmdb

Enriches movie entries with metadata from The Movie Database (TMDb). Searches by parsed title and year and caches results.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TMDb API v3 key |
| `cache_ttl` | string | no | `24h` | How long to cache search results |

## Fields set on entry

### Provider-specific (always)

| Field | Type | Description |
|-------|------|-------------|
| `tmdb_id` | int | TMDb movie ID |

### Standard fields (always)

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Movie title from TMDb |
| `description` | string | Plot summary |
| `published_date` | string | Release date (`YYYY-MM-DD`) |
| `enriched` | bool | `true` — TMDb successfully enriched this entry |
| `video_year` | int | Release year |
| `video_original_title` | string | Original language title when different from `title` |
| `video_language` | string | Original language display name (e.g. `English`) |
| `video_country` | string | First production country name (e.g. `United States of America`) |
| `video_rating` | float64 | Average user rating (0–10) |
| `video_votes` | int | Number of votes |
| `video_popularity` | float64 | TMDb popularity score |
| `video_runtime` | int | Runtime in minutes |
| `video_poster` | string | Poster image URL (`w500` size) |
| `video_cast` | []string | Top 10 actor names in billing order |
| `video_trailers` | []string | YouTube trailer URLs |
| `video_content_rating` | string | US content rating (e.g. `PG-13`, `R`) |
| `video_imdb_id` | string | IMDb ID (e.g. `tt1375666`) |
| `video_aliases` | []string | Alternative titles |
| `video_genres` | []string | Genre names |
| `movie_tagline` | string | Tagline |

## Example

```python
task("movies", [
    plugin("rss", url="https://example.com/feed"),
    plugin("movies", static=["Inception"]),
    plugin("metainfo_tmdb", api_key="YOUR_API_KEY"),
    plugin("pathfmt", path="/media/movies/{title} ({video_year})", field="download_path"),
])
```

## Notes

- Free API keys at [themoviedb.org/settings/api](https://www.themoviedb.org/settings/api).
- Only annotates entries whose title can be parsed as a movie (title + year). Entries without a parseable year are skipped.
- Results are cached in `pipeliner.db` in the same directory as the config file.
- Use `enriched` (not `tmdb_id`) to check whether TMDb successfully found metadata: `plugin("require", fields=["enriched"])`.
