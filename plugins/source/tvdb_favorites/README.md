# tvdb_favorites

Fetches shows from a TheTVDB user's favorites list and emits one entry per show. Entries carry the show name and a canonical TheTVDB URL, making them suitable as upstream nodes feeding `discover`, or as title sources inside `series.list`.

Use as a standalone `input()` source node, or inside `series.list`, `movies.list`, or `discover.search` config keys.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `api_key` | yes | — | TheTVDB v4 API key |
| `user_pin` | yes | — | User PIN from thetvdb.com account settings |

## Fields set on each entry

TheTVDB's series-by-id response already carries genres, network, country, language, poster, overview, and a popularity score. `tvdb_favorites` surfaces these via the standard `video_*` / `series_*` fields so list-only pipelines don't need a downstream `metainfo_tvdb` step to filter on them.

Note: TheTVDB's `Score` is a popularity ranking, not a 0-10 user rating — it goes into `video_popularity`, and `video_rating` stays empty unless a downstream provider with a user-rating field (TMDb, Trakt) enriches the entry.

| Field | Description |
|-------|-------------|
| `source` | Always `tvdb_favorites:favorites` |
| `enriched` | `true` — set so downstream metainfo nodes know the entry already carries provider data |
| `title` | Series name |
| `media_type` | `"series"` (TVDB only catalogs TV) |
| `description` | Series overview |
| `video_year` | Premiere year (parsed from `first_aired` when available, else from `year`) |
| `video_language` | Original language name (e.g. `"English"`) |
| `video_country` | Country of origin name (e.g. `"United States"`) |
| `video_genres` | List of genre names |
| `video_popularity` | TheTVDB popularity score (NOT a 0-10 user rating) |
| `video_poster` | Poster image URL |
| `series_network` | Originating network |
| `series_first_air_date` | Premiere date as `time.Time` |
| `tvdb_id` | TheTVDB series ID |
| `tvdb_slug` | TheTVDB slug (e.g. `"breaking-bad"`) |
| `tvdb_year` | Premiere year as the raw string TheTVDB returned (for back-compat) |

## Example — dynamic title source for the series filter

```python
src    = input("rss", url="https://example.com/rss/shows")
seen   = process("seen", upstream=src)
meta   = process("metainfo_file", upstream=seen)
series = process("series", upstream=meta,
    tracking="strict", quality="720p+",
    list=[{"name": "tvdb_favorites", "api_key": "YOUR_TVDB_API_KEY", "user_pin": "YOUR_TVDB_USER_PIN"}])
output("deluge", upstream=series, host="localhost", password="changeme")
pipeline("tv-favorites", schedule="1h")
```

## Notes

- API keys and user PINs are available at [thetvdb.com/api-information](https://thetvdb.com/api-information).
- On the first run this plugin makes N+1 API calls: one for the favorites ID list and one per show to resolve its name and slug. Results are cached by the calling plugin (`series`) according to its TTL setting.

## DAG role

`tvdb_favorites` has `Role=source`. It is used inside `series.list`, and can also be used as a standalone `input()` node feeding `discover` or any other DAG pipeline:

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `media_type` (= `"series"` — TVDB only catalogs TV), `source`, `tvdb_id` |
| MayProduce | `enriched`, `description`, `video_year`, `video_language`, `video_country`, `video_genres`, `video_popularity`, `video_poster`, `series_network`, `series_first_air_date`, `tvdb_year`, `tvdb_slug` |
| Requires | — |
