# metainfo_tvdb

Enriches series entries with metadata from TheTVDB. Searches by parsed series name and caches results. Fields missing from the search response (genres, language) are filled in automatically via a second call to the series extended endpoint. If a specific season and episode are parsed, episode-level detail is also fetched.

All results are cached in `pipeliner.db` to avoid redundant API calls across runs.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `cache_ttl` | string | no | `24h` | How long to cache results |

## Fields set on entry

### Provider-specific (always)

| Field | Type | Description |
|-------|------|-------------|
| `tvdb_id` | string | TheTVDB series ID |
| `tvdb_slug` | string | URL slug (use to build `https://thetvdb.com/series/{slug}`) |

### Series-level standard fields (always)

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Series name from TheTVDB |
| `description` | string | Series overview |
| `published_date` | string | Date of first broadcast (`YYYY-MM-DD`) |
| `enriched` | bool | `true` — TVDB successfully enriched this entry |
| `video_year` | int | Premiere year |
| `video_language` | string | Original language (e.g. `English`) |
| `video_original_title` | string | Original-language title when different from `title` |
| `video_country` | string | Country of origin (e.g. `usa`) |
| `video_genres` | []string | Genre names (e.g. `["Drama", "Crime"]`) |
| `video_rating` | float64 | Popularity score |
| `video_poster` | string | Poster image URL |
| `video_cast` | []string | Actor names in display order |
| `video_content_rating` | string | Content rating (e.g. `TV-MA`, `TV-14`) |
| `video_trailers` | []string | Trailer URLs |
| `video_aliases` | []string | Alternative titles |
| `series_network` | string | Originating network (e.g. `AMC`) |
| `series_status` | string | Series status (e.g. `Ended`, `Continuing`) |
| `series_first_air_date` | Date of first broadcast |
| `series_last_air_date` | Date of most recent episode |
| `series_next_air_date` | Next scheduled air date, if known |

### Episode-level (when season and episode are parsed)

| Field | Description |
|-------|-------------|
| `tvdb_episode_id` | TheTVDB internal numeric episode ID |
| `series_season` | Season number |
| `series_episode` | Episode number |
| `series_episode_id` | Episode identifier string (e.g. `S02E05`) |
| `series_episode_title` | Episode title |
| `series_episode_description` | Episode overview |
| `series_episode_air_date` | Episode air date |
| `series_episode_image` | Episode still/thumbnail URL |
| `video_runtime` | Episode runtime in minutes |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `enriched`, `title`, `description`, `video_year`, `video_language`, `video_original_title`, `video_country`, `video_genres`, `video_rating`, `video_votes`, `video_poster`, `video_cast`, `video_content_rating`, `video_runtime`, `video_trailers`, `video_aliases`, `series_network`, `series_status`, `series_first_air_date`, `series_last_air_date`, `series_next_air_date`, `series_episode_id`, `series_episode_title`, `series_episode_description`, `series_episode_air_date`, `series_episode_image`, `tvdb_id`, `tvdb_slug`, `tvdb_episode_id` |
| Requires | — |

## Example

```python
src  = input("rss", url="https://example.com/rss")
seen = process("seen",          upstream=src)
ep   = process("metainfo_file", upstream=seen)
tvdb = process("metainfo_tvdb",   upstream=ep, api_key=env("TVDB_KEY"))
req  = process("require",         upstream=tvdb, fields=["enriched"])
fmt  = process("pathfmt",         upstream=req,
               path="/media/tv/{title}/Season {series_season:02d}",
               field="download_path")
output("transmission", upstream=fmt, host="localhost")
pipeline("tv-tvdb", schedule="1h")
```

## Notes

- API keys are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- Only annotates entries whose title parses as a series episode. Non-episode titles are skipped.
- Language codes (e.g. `eng`) are automatically mapped to display names (e.g. `English`).
- The `video_genres` field is a string slice; use `{{join ", " (index .Fields "video_genres")}}` in templates.
- Date fields (`series_first_air_date`, `series_last_air_date`, `series_next_air_date`, `series_episode_air_date`) are date values. Use `{{formatdate "January 2, 2006" .series_first_air_date}}` in templates and `< daysago(n)` / `> daysago(n)` in conditions.
- Use `enriched` (not `tvdb_id`) to check whether TVDB successfully found metadata: `process("require", upstream=…, fields=["enriched"])`.
