# metainfo_tmdb

Enriches movie entries with metadata from The Movie Database (TMDb). The film is resolved by an id the entry already carries where there is one, and otherwise by searching for the parsed title and year. Results are cached.

## Which film gets looked up

In order:

1. **A TMDb id on the entry** — `tmdb_id`, `trakt_tmdb_id` or `jackett_tmdb_id`, in that order of trust. Fetched directly; no search.
2. **An IMDb id on the entry** — `video_imdb_id`, `trakt_imdb_id` or `jackett_imdb_id`, mapped onto TMDb's own id through `/find`. The mapping is cached, since neither id ever changes.
3. **A title search**, with the year as a filter, exactly as before.

A title search ranks by popularity and the year it is given is whatever a release name claimed, so it picks the wrong film for every title two films share — *Michael* (1996) against *Michael* (2026) is the case this was found on. An id is the film.

Every shape an id arrives in is read: a string, a JSON number, a `float64` after a round trip through the store. That matters more than it sounds — the id used to be read as `e.Fields["trakt_tmdb_id"].(int)`, so a plain `tmdb_id` was ignored, an indexer's id was ignored, and anything pushed at the ingest API (whose fields come straight from JSON) arrived as a `float64` and was ignored too. The on-demand request flow, the one most likely to be handed an id, was the one taking the search path.

An id TMDb has no film for falls through to the search rather than losing the entry its enrichment, and that answer is cached too.

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
| `video_homepage` | string | Official site URL |
| `movie_tagline` | string | Tagline |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `enriched`, `title`, `media_type` (= `"movie"`), `movie_title` (**deprecated** — use `title`), `movie_tagline`, `video_year`, `video_language`, `video_original_title`, `video_country`, `video_genres`, `video_rating`, `video_poster`, `video_runtime`, `video_aliases`, `video_imdb_id`, `video_popularity`, `video_votes`, `video_homepage`, `tmdb_id` |
| Requires | — |

## Lookup strategy

The plugin resolves movies in this order:

1. **By ID** — if the entry carries `trakt_tmdb_id` (set by `trakt_list` or `metainfo_trakt`), the movie is fetched directly by TMDb ID. No search is performed, so there is no ambiguity even when multiple films share the same title (e.g. "Michael" 1996 vs 2026).
2. **By title + year** — the title is parsed from the entry (torrent release format or plain Trakt title). If no year is found in the title but `video_year` is present (set by `trakt_list`), that year is used as the search hint.
3. **Year-less retry** — if the year-filtered search returns nothing (off-by-one year, regional difference, etc.), the search is retried without a year filter. Results are filtered to candidates whose title fuzzy-matches the searched title, preventing popularity-ranked mismatches (e.g. "Mother's Day" appearing for a search of "Mother").

## Example

```python
src  = input("rss", url="https://example.com/feed")
flt  = process("movies", upstream=src, static=["Inception"])
meta = process("metainfo_tmdb", upstream=flt, api_key=env("TMDB_KEY"))
fmt  = process("pathfmt", upstream=meta, path="/media/movies/{title} ({video_year})", field="download_path")
output("qbittorrent", upstream=fmt, host="localhost")
pipeline("movies", schedule="1h")
```

With a Trakt watchlist as the movie source the plugin resolves the correct film even when the title is ambiguous:

```python
trakt_src = input("trakt_list", client_id=env("TRAKT_ID"), type="movies", list="watchlist")
flt  = process("movies",        upstream=rss, list=[trakt_src])
meta = process("metainfo_tmdb", upstream=flt, api_key=env("TMDB_KEY"))
```

## Notes

- Free API keys at [themoviedb.org/settings/api](https://www.themoviedb.org/settings/api).
- Entries from `trakt_list` carry both `trakt_tmdb_id` and `video_year`, which are used to resolve the correct film unambiguously.
- Plain Trakt titles (no quality markers, no year suffix) are supported as search terms when `video_year` is present.
- Results are cached in `pipeliner.db` in the same directory as the config file.
- Use `enriched` (not `tmdb_id`) to check whether TMDb successfully found metadata: `process("require", upstream=…, fields=["enriched"])`.
