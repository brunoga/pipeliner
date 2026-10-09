# metainfo_tvdb

Enriches series entries with metadata from TheTVDB. The series is resolved by the entry's `tvdb_id` where it already carries one, and otherwise by searching for the parsed series name. Fields missing from the search response (genres, language) are filled in automatically via a second call to the series extended endpoint. If a specific season and episode are parsed, episode-level detail is also fetched.

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
| `video_popularity` | float64 | TheTVDB popularity score (NOT a 0-10 user rating — use TMDb or Trakt enrichment for that) |
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
| Produces | `enriched`, `title`, `media_type` (= `"series"` — TVDB is series-only), `description`, `video_year`, `video_language`, `video_original_title`, `video_country`, `video_genres`, `video_popularity`, `video_poster`, `video_cast`, `video_content_rating`, `video_runtime`, `video_trailers`, `video_aliases`, `series_network`, `series_status`, `series_first_air_date`, `series_last_air_date`, `series_next_air_date`, `series_episode_id`, `series_episode_title`, `series_episode_description`, `series_episode_air_date`, `series_episode_image`, `tvdb_id`, `tvdb_slug`, `tvdb_episode_id` |
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

## Which series gets looked up

An `tvdb_id` already on the entry is used in preference to a name search, and nothing is spent proving it: the extended record is what the fields are built from in any case, so fetching it both confirms the id and warms the cache. Where there is no id, or TheTVDB does not answer for it, the name search runs exactly as before.

The id is the better answer wherever something upstream knows it. [`series_gaps`](../../gaps/) computed the gap for a specific series and [`discover`](../../discover/) carries that identity onto the release it found; a search knows only the string, and a string is not always enough:

- `Tomb Raider` names two different shows on TheTVDB — the 2026 series and the anime — so a search picks one of them by relevance, and `pickSeries`' exact-title preference cannot break the tie because both titles are exactly that.
- A title whose punctuation defeats the search engine returns nothing at all: `Tomb Raider: The Legend of Lara Croft` yields zero results, while the short name yields seven.

Getting this wrong is not a thin notification but a confident one about the wrong show, carrying its poster, its link and its episode titles.

## Notes

- API keys are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- Only annotates entries whose title parses as a series episode. Non-episode titles are skipped.
- Language codes (e.g. `eng`) are automatically mapped to display names (e.g. `English`).
- The `video_genres` field is a string slice; use `{{join ", " (index .Fields "video_genres")}}` in templates.
- Date fields (`series_first_air_date`, `series_last_air_date`, `series_next_air_date`, `series_episode_air_date`) are date values. Use `{{formatdate "January 2, 2006" .series_first_air_date}}` in templates and `< daysago(n)` / `> daysago(n)` in conditions.
- Use `enriched` (not `tvdb_id`) to check whether TVDB successfully found metadata: `process("require", upstream=…, fields=["enriched"])`.
