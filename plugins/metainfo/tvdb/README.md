# metainfo_tvdb

Enriches series entries with metadata from TheTVDB. Searches by parsed series name and caches results. Fields missing from the search response (genres, language) are filled in automatically via a second call to the series extended endpoint. If a specific season and episode are parsed, episode-level detail is also fetched.

All results are cached in `pipeliner.db` to avoid redundant API calls across runs.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `cache_ttl` | string | no | `24h` | How long to cache results |

## Fields set on entry

### Series-level (always)

| Field | Type | Description |
|-------|------|-------------|
| `tvdb_id` | string | TheTVDB series ID |
| `tvdb_series_name` | string | Series name from TheTVDB |
| `tvdb_series_year` | string | Premiere year |
| `tvdb_overview` | string | Series overview |
| `tvdb_slug` | string | URL slug (use to build `https://thetvdb.com/series/{slug}`) |
| `tvdb_network` | string | Originating network (e.g. `AMC`) |
| `tvdb_language` | string | Original language (e.g. `English`) |
| `tvdb_country` | string | Country of origin (e.g. `usa`) |
| `tvdb_genres` | []string | Genre names (e.g. `["Drama", "Crime"]`) |
| `tvdb_poster` | string | Poster image URL |
| `tvdb_first_air_date` | time.Time | Date of first broadcast |
| `tvdb_last_air_date` | time.Time | Date of most recent episode |
| `tvdb_next_air_date` | time.Time | Next scheduled air date (if known) |
| `tvdb_status` | string | Series status (e.g. `Ended`, `Continuing`) |
| `tvdb_content_rating` | string | Content rating (e.g. `TV-MA`, `TV-14`) |
| `tvdb_trailers` | []string | Trailer URLs |
| `tvdb_score` | float64 | Popularity score |
| `tvdb_aliases` | []string | Alternative titles |
| `tvdb_cast` | []string | Actor names in display order |

### Episode-level (when season and episode are parsed)

| Field | Type | Description |
|-------|------|-------------|
| `tvdb_episode_id` | int | TheTVDB episode ID |
| `tvdb_episode_name` | string | Episode title |
| `tvdb_air_date` | string | Episode air date (`YYYY-MM-DD`) |
| `tvdb_episode_overview` | string | Episode overview |
| `tvdb_episode_runtime` | int | Episode runtime in minutes |
| `tvdb_episode_image` | string | Episode still/thumbnail URL |

## Example

```yaml
tasks:
  tv:
    rss:
      url: "https://example.com/feed"
    metainfo_tvdb:
      api_key: YOUR_TVDB_API_KEY
    condition:
      rules:
        - reject: 'tvdb_language != "" and tvdb_language != "English"'
        - reject: 'tvdb_genres contains "Documentary"'
        - reject: 'tvdb_first_air_date != "" and tvdb_first_air_date < daysago(365)'
    premiere:
      quality: 720p+
```

## Notes

- API keys are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- Only annotates entries whose title parses as a series episode. Non-episode titles are skipped.
- Language codes (e.g. `eng`) are automatically mapped to display names (e.g. `English`).
- The `tvdb_genres` field is a string slice; use `{{join ", " (index .Fields "tvdb_genres")}}` in templates.
