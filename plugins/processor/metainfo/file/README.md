# metainfo_file

Parses the entry title (filename) and annotates **all** detectable metadata in a single pass: classifies the entry as series, movie, or other; sets the appropriate series/movie fields; and sets the quality fields (resolution, source, codec, audio, color range).

Takes no config.

> **Why this plugin:** without `metainfo_file`, a mixed feed needs `metainfo_series` + `metainfo_quality` chained together, and there is no equivalent for movies at all. `metainfo_file` is the single canonical "extract everything possible from the filename" step.

## Classification

The plugin sets `media_type` to one of:

| `media_type` | Set when | Series fields | Movie fields |
|---|---|---|---|
| `"series"` | Title parses as a TV episode (SxxExx, dates, etc.) | ✓ | — |
| `"movie"`  | Title parses as a movie (year + quality marker, etc.), **or** the title is unparseable but `video_year` was set upstream (typically by `trakt_list`) | — | ✓ |
| unset      | Neither parser matched and no `video_year` hint is present | — | — |

Series is tried first because the series parser requires an explicit episode pattern. A title like `Show.2023.S01E01.720p` matches both parsers, but the unambiguous episode marker makes it a series.

**List-source fallback.** When the entry title is a clean string with no year or quality marker (e.g. `"Avengers"` from `trakt_list`), the filename parser can't anchor the title boundary and returns no match. In that case `metainfo_file` falls back to the upstream-set `video_year`: it normalises the raw title (lowercasing dots and underscores, title-casing words) and stamps the entry as a movie. This makes list-sourced pipelines work without requiring a downstream API enricher just to classify entries.

Quality fields are set whenever any quality dimension is detected, **regardless of classification** — a "movie" entry still gets `video_resolution`, `codec`, etc.

## Fields set on entry

### Always (when applicable)

| Field | Type | Example | Description |
|---|---|---|---|
| `media_type` | string | `series` / `movie` | Classification, unset for non-video text |

### When classified as series

| Field | Type | Example |
|---|---|---|
| `title` | string | `Breaking Bad` |
| `series_season` | int | `1` |
| `series_episode` | int | `1` |
| `series_episode_id` | string | `S01E01`, `2023-11-15`, `EP123` |
| `series_double_episode` | int | `2` (for `S01E01E02`) |
| `series_service` | string | `AMZN`, `Netflix` |
| `series_container` | string | `mkv` |

### When classified as movie

| Field | Type | Example |
|---|---|---|
| `title` | string | `Avengers` |
| `movie_title` | string | `Avengers` |
| `video_year` | int | `2012` |

### Quality + release markers (always, when any signal detected)

| Field | Type | Example |
|---|---|---|
| `video_quality` | string | `1080p BluRay H264 Atmos` |
| `video_resolution` | string | `1080p` |
| `video_source` | string | `BluRay` |
| `video_is_3d` | bool | `false` |
| `video_proper` | bool | `true` (set when the title contains PROPER, for either series or movies) |
| `video_repack` | bool | `true` (set when the title contains REPACK or RERIP) |
| `codec` | string | `H264` |
| `audio` | string | `Atmos` |
| `color_range` | string | `HDR10` |
| `quality_resolution` | string | numeric rank (for sorting) |
| `quality_source` | string | numeric rank (for sorting) |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| MayProduce | All series, movie, and quality fields listed above plus `media_type` |
| Requires | — |

## Examples

### Pure annotation — works on any feed

```python
src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)
output("print", upstream=meta)
pipeline("inspect", schedule="1h")
```

After `metainfo_file`, every entry that contains parseable metadata in its filename will have `media_type` and the relevant fields populated.

### Routing a mixed feed

Use `route()` downstream to dispatch by classification:

```python
src  = input("rss", url="https://example.com/mixed-feed")
meta = process("metainfo_file", upstream=src)

routes = route(meta,
    series = "media_type == 'series'",
    movies = "media_type == 'movie'",
    other  = "media_type == ''")

series_filter = process("series", upstream=routes.series, static=["Breaking Bad"])
movies_filter = process("movies", upstream=routes.movies, static=["Avengers"])

output("transmission", upstream=series_filter, host="localhost")
output("transmission", upstream=movies_filter, host="localhost")
output("print",        upstream=routes.other)
pipeline("mixed", schedule="30m")
```

### Using `media_type` in conditions

```python
src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)

# Reject everything that isn't a video.
videos = process("condition", upstream=meta,
    reject = "media_type == ''")

output("print", upstream=videos)
pipeline("videos-only", schedule="1h")
```

## Notes

- `metainfo_file` does the same title-parsing work as `metainfo_series` and `metainfo_quality` combined, plus movie detection. It does **not** call any external APIs — for richer enrichment (cast, overview, ratings) chain `metainfo_tvdb` or `metainfo_tmdb` after it.
- Movie detection without a configured title list is best-effort: anything with a year followed by a quality marker is classified as movie. Use the `movies` filter (with a title list) for confident matching.
- Entries already rejected or failed upstream are skipped — `metainfo_file` never resurrects them and never produces side effects on them.
