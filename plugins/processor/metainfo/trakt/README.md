# metainfo_trakt

Annotates entries with metadata from Trakt.tv. The item is resolved by an id the entry already carries where there is one, and otherwise by searching for the parsed show or movie name. Results are cached either way.

## Which item gets looked up

An id on the entry is tried first — `trakt_id`, then IMDb, then TMDb, then TheTVDB — through Trakt's id search. Which one an entry has depends on what ran upstream: `trakt_list` leaves its own `trakt_id`, `metainfo_tvdb` leaves a `tvdb_id`, an indexer may have published an IMDb id. The first namespace Trakt answers for wins; one it has no item for costs the entry nothing, because the next namespace and then the name search still follow. Both kinds of answer are cached, under a key that records which question was asked.

A name search knows only a string, and a string is not an identity. It cannot separate two shows called *Tomb Raider* or two films called *Michael*, and Trakt answers it with whichever is more popular.

When the name search is what runs, the result is now chosen with the year in hand:

1. Exact title match (normalized) whose year is compatible — within one, since regional windows disagree by one.
2. Exact title match, any year.
3. Any result with a compatible year.
4. Trakt's own relevance ranking.

The year is a preference rather than a filter, because a release name frequently omits it and occasionally gets it wrong. It comes from the parsed release name, falling back to `video_year` for a clean list title that has no year of its own.

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
| `video_rating` | float64 | Community user rating (0–10) |
| `video_votes` | int | Number of votes behind `video_rating` |
| `video_imdb_id` | string | IMDb ID |
| `video_genres` | []string | Genre names |
| `video_runtime` | int | Runtime in minutes (per-episode for shows) |
| `video_language` | string | Original language display name (e.g. `English`) |
| `video_country` | string | Country of origin display name (e.g. `United States`) |
| `video_content_rating` | string | Certification (e.g. `TV-MA`, `PG-13`) |
| `video_trailers` | []string | Trailer URLs (Trakt returns a single trailer; surfaced as a one-element slice for consistency with metainfo_tvdb / metainfo_tmdb) |
| `video_homepage` | string | Official site URL |

### Series-only (shows)

| Field | Type | Description |
|-------|------|-------------|
| `series_network` | string | Originating network (e.g. `AMC`) |
| `series_status` | string | Status (e.g. `returning series`, `ended`) |
| `series_first_air_date` | time.Time | Premiere date |

### Movie-only

| Field | Type | Description |
|-------|------|-------------|
| `movie_tagline` | string | Marketing tagline |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `enriched`, `title`, `media_type` (= `"series"` or `"movie"` per `type=`), `description`, `video_year`, `video_genres`, `video_rating`, `video_votes`, `video_language`, `video_country`, `video_runtime`, `video_trailers`, `video_content_rating`, `video_homepage`, `video_imdb_id`, `video_poster`, `movie_tagline`, `series_network`, `series_status`, `series_first_air_date`, `trakt_id`, `trakt_slug`, `trakt_tmdb_id`, `trakt_tvdb_id` |
| Requires | — |

## Example

```python
src  = input("rss", url="https://example.com/rss")
meta = process("metainfo_trakt", upstream=src, client_id=env("TRAKT_ID"), type="movies")
req  = process("require",        upstream=meta, fields=["enriched"])
flt  = process("condition",      upstream=req,  accept="video_rating >= 7.0")
output("qbittorrent", upstream=flt, host="localhost")
pipeline("movies-trakt", schedule="1h")
```

## Notes

- Results are cached in `pipeliner.db` in the same directory as the config file.
- Use `enriched` (not `trakt_id`) to check whether Trakt successfully found metadata: `process("require", upstream=…, fields=["enriched"])`.
