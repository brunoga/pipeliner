# Configuration

Pipeliner uses a single Starlark file (default `config.star`). Pass a custom path with `--config`.

## Top-level structure

```python
# variables — ordinary Starlark assignments
tv_root = "/media/tv"
tvdb_key = env("TVDB_API_KEY")   # read from environment

# helper functions — reusable pipeline chains
def enrich_and_format(upstream, dest):
    meta = process("metainfo_tvdb", upstream=upstream, api_key=tvdb_key)
    req  = process("require",       upstream=meta, fields=["enriched"])
    fmt  = process("pathfmt",       upstream=req,
                   path=dest + "/{title}/Season {series_season:02d}",
                   field="download_path")
    return fmt

# pipelines — connect nodes, call pipeline() to register
src    = input("rss",            url="https://example.com/rss")
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)   # required by series/movies/premiere
series = process("series",        upstream=meta, static=["My Show"])
output("transmission", upstream=enrich_and_format(series, tv_root),
       host="localhost", path="{download_path}")
pipeline("my-pipeline", schedule="1h")          # interval
# pipeline("cron-task",  schedule="0 6 * * *")  # cron expression
```

## Database

Pipeliner automatically maintains a single SQLite database named `pipeliner.db` in the same directory as the config file. All stateful plugins (`seen`, `series`, `movies`, `premiere`, `discover`, the Trakt/TVDB caches, and the cross-task lists) share this one file. No configuration is needed — the file is created on first run and grows as state accumulates.

## Variables

Variables are ordinary Starlark assignments at the top of the file. They are useful for secrets and paths shared across multiple pipelines.

```python
tv_root = "/media/tv"

src = input("rss", url="https://example.com/rss")
fmt = process("pathfmt", upstream=src, path=tv_root + "/{title}", field="download_path")
output("transmission", upstream=fmt, host="localhost")
pipeline("my-pipeline")
```

## Helper functions

Helper functions let you package a reusable chain of nodes and call it by name. They accept an upstream node and return the terminal node of the chain, so they can be composed or called inline.

```python
def common_source(feed_url, local_seen=False):
    src  = input("rss", url=feed_url)
    seen = process("seen", upstream=src, local=local_seen)
    return seen

def common_sink(upstream, dest, host="localhost"):
    fmt = process("pathfmt", upstream=upstream,
                  path=dest + "/{title}", field="download_path")
    output("transmission", upstream=fmt, host=host, path="{download_path}")

# compose helper functions
common_sink(
    process("series", upstream=common_source("https://example.com/rss/tv"),
            static=["Breaking Bad"]),
    "/media/tv",
)
pipeline("tv-shows", schedule="30m")
```

## Pipelines and schedules

A pipeline connects nodes (sources, processors, sinks) and registers them with an optional schedule. In `daemon` mode, pipelines run according to their schedule.

```python
pipeline("hourly-pipeline",  schedule="1h")
pipeline("daily-pipeline",   schedule="24h")
pipeline("morning-pipeline", schedule="0 6 * * *")   # daily at 06:00
```

Pipelines without a `schedule=` argument are not run automatically by the daemon.

## Entry fields

Every entry has a title, a URL, and a set of named fields that plugins read and write. Use the `{field}` syntax to reference them in path templates, conditions, and command strings (see [Pattern syntax](#pattern-syntax) below).

Fields are grouped by prefix — the prefix tells you what kind of data the field holds.

### Universal fields

| Field | Set by | Description |
|-------|--------|-------------|
| `title` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt`, `metainfo_file` | Canonical enriched display name |
| `media_type` | `metainfo_file` | Classification: `"series"`, `"movie"`, or unset. Useful as a `route()` dispatch key. |
| `description` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt` | Synopsis / overview |
| `published_date` | `metainfo_tvdb`, `metainfo_tmdb`, `rss` | Release or premiere date |
| `enriched` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt` | `true` when an external provider enriched this entry |
| `raw_title` | all sources | Original entry title (torrent filename or feed item title) |

### Video fields — movies and series

| Field | Set by | Description |
|-------|--------|-------------|
| `video_year` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt`, `metainfo_file` | Release or premiere year |
| `video_language` | `metainfo_tvdb` | Original language (e.g. `English`) |
| `video_country` | `metainfo_tvdb` | Country of origin (e.g. `United States`) |
| `video_genres` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt` | Genre list |
| `video_rating` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt` | Audience rating (0–10) |
| `video_poster` | `metainfo_tvdb` | Poster image URL |
| `video_cast` | `metainfo_tvdb` | Cast list |
| `video_runtime` | `metainfo_tvdb`, `metainfo_tmdb` | Runtime in minutes |
| `video_quality` | `metainfo_file` | Full quality string including 3D format when present (e.g. `BD3D 1080p BluRay H.265`) |
| `video_resolution` | `metainfo_file` | Resolution (e.g. `1080p`, `720p`) |
| `video_source` | `metainfo_file` | Source (e.g. `BluRay`, `WEB-DL`, `HDTV`) |
| `video_is_3d` | `metainfo_file` | `true` when any 3D format marker is detected (3D, SBS, HOU, BD3D, etc.) |
| `video_proper` | `metainfo_file` | `true` for PROPER releases (applies to series and movies) |
| `video_repack` | `metainfo_file` | `true` for REPACK releases (applies to series and movies) |
| `video_imdb_id` | `metainfo_tmdb`, `metainfo_trakt` | IMDb ID (e.g. `tt1375666`) |

### Series fields

| Field | Set by | Description |
|-------|--------|-------------|
| `series_season` | `metainfo_file` | Season number |
| `series_episode` | `metainfo_file` | Episode number |
| `series_episode_id` | `metainfo_file` | Episode ID string (e.g. `S02E05`) |
| `series_network` | `metainfo_tvdb` | Broadcasting network |
| `series_status` | `metainfo_tvdb` | Series status (e.g. `Ended`, `Continuing`) |
| `series_first_air_date` | `metainfo_tvdb` | Series premiere date |
| `series_episode_title` | `metainfo_tvdb` | Episode title |
| `series_episode_air_date` | `metainfo_tvdb` | Episode air date |
| `series_service` | `metainfo_file` | Streaming service tag from filename (e.g. `AMZN`, `NF`) |

### Movie fields

| Field | Set by | Description |
|-------|--------|-------------|
| `movie_title` | `metainfo_tmdb`, `metainfo_file` | **Deprecated** — duplicates `title` when `media_type == "movie"`; use `title` |
| `movie_tagline` | `metainfo_tmdb` | Movie tagline |

### Torrent fields

| Field | Set by | Description |
|-------|--------|-------------|
| `torrent_link_type` | `jackett`, `rss` | `"torrent"` or `"magnet"` — used by `metainfo_torrent`, `metainfo_magnet`, and `deluge` to route handling without a URL fetch |
| `torrent_info_hash` | `metainfo_torrent`, `metainfo_magnet`, `torrentalive` | SHA-1 info hash (hex) |
| `torrent_file_size` | `metainfo_torrent`, `metainfo_magnet`, `jackett` | Total size in bytes |
| `torrent_file_count` | `metainfo_torrent`, `metainfo_magnet` | Number of files |
| `torrent_files` | `metainfo_torrent`, `metainfo_magnet` | List of file paths |
| `torrent_seeds` | `torrentalive`, `rss`, `jackett` | Seed count |
| `torrent_leechers` | `jackett` | Leecher count |
| `torrent_announce` | `metainfo_torrent`, `metainfo_magnet` | Primary tracker URL |

### File fields

| Field | Set by | Description |
|-------|--------|-------------|
| `file_name` | `filesystem` | Filename (without directory) |
| `file_extension` | `filesystem` | File extension including dot (e.g. `.torrent`) |
| `file_location` | `filesystem` | Absolute path on disk |
| `file_size` | `filesystem` | File size in bytes |
| `file_modified_time` | `filesystem` | Last-modified time |

### RSS fields

| Field | Set by | Description |
|-------|--------|-------------|
| `rss_feed` | `rss` | Feed URL |
| `rss_guid` | `rss` | Feed item GUID |
| `rss_link` | `rss` | Article/item link URL |

### Provider-specific (kept alongside standard fields)

| Field | Set by | Description |
|-------|--------|-------------|
| `tvdb_id` | `metainfo_tvdb` | TheTVDB series ID |
| `tvdb_slug` | `metainfo_tvdb` | TheTVDB URL slug |
| `tvdb_episode_id` | `metainfo_tvdb` | TheTVDB internal episode ID |
| `tmdb_id` | `metainfo_tmdb` | TMDb movie/series ID |
| `trakt_id` | `metainfo_trakt` | Trakt internal ID |
| `trakt_slug` | `metainfo_trakt` | Trakt URL slug |
| `trakt_tmdb_id` | `metainfo_trakt` | TMDb cross-reference ID |
| `trakt_tvdb_id` | `metainfo_trakt` | TheTVDB cross-reference ID |
| `download_path` | `pathfmt` (with `field="download_path"`) | Rendered, scrubbed download path |

Custom fields can be set with the [`set`](../plugins/processor/modify/set/README.md) plugin and used in any pattern or condition.

## Pattern syntax

String values in `pathfmt`, `exec`, `print`, `set`, and download-client path configs support a simple interpolation syntax:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `{field}` | Insert field value | `{title}` |
| `{field:format}` | Printf-formatted field | `{series_season:02d}` |

For advanced formatting, Go template syntax is also supported: `{{.field}}`, `{{.title | upper}}`, `{{printf "%02d" .series_season}}`, etc. Template functions like `upper`, `lower`, `scrub`, `join`, `replace`, `formatdate`, and others are available — see the [Plugin Development Guide](../PLUGIN_DEVELOPMENT_GUIDE.md#template-helper-functions) for the full list.

## Boolean expressions

The same infix expression syntax is used by the `condition` plugin and `route()` port conditions:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `field > value` | Numeric comparison | `video_rating > 7.0` |
| `field == "str"` | String equality | `video_source == "CAM"` |
| `field contains "str"` | Substring or slice contains | `video_genres contains "Documentary"` |
| `field matches "regex"` | Regexp match | `title matches "\\d{4}"` |
| `expr and expr` | Logical AND (`&&` also works) | `video_rating > 7.0 and video_source != "CAM"` |
| `expr or expr` | Logical OR (`\|\|` also works) | `video_source == "CAM" or video_source == "TS"` |
| `not expr` | Logical NOT (`!` also works) | `not video_source == "CAM"` |

Go template syntax (`{{gt .field value}}`, `{{if ...}}`) is also supported for more complex expressions.

## Pipelines

Pipelines are built by connecting nodes with `input()`, `process()`, `merge()`, `output()`, and `pipeline()`.

```python
src     = input("rss", url="https://example.com/rss")
meta    = process("metainfo_file", upstream=src)
flt     = process("series", upstream=meta, static=["My Show"], quality="720p+")
output("transmission", upstream=flt, host="localhost")
pipeline("my-pipeline", schedule="1h")
```

| Feature | How |
|---------|-----|
| Merge multiple sources | `merge(src1, src2)` — deduplicates by URL |
| Fan-out to multiple sinks | Pass the same upstream node to multiple `output()` calls |
| Conditional branching | `route(upstream, tv="expr", movies="expr")` — mutually exclusive ports |
| Parallel branches | Wire the same node to multiple independent `process()` chains |

## Example configs

### TV / series

- [`tv-series-deluge.star`](tv-series-deluge.star) — static show list → series filter → Deluge
- [`trakt-shows-transmission.star`](trakt-shows-transmission.star) — Trakt watchlist + trending → two pipelines → Transmission
- [`tvdb-favorites-deluge.star`](tvdb-favorites-deluge.star) — TheTVDB favorites → series filter → TVDB enrichment → Deluge
- [`jackett-tv-transmission.star`](jackett-tv-transmission.star) — active Jackett search driven by Trakt watchlist → Transmission
- [`tv-two-feeds.star`](tv-two-feeds.star) — **merge** two RSS feeds → dedup → series → Transmission

### Movies

- [`movie-downloads.star`](movie-downloads.star) — static list + TMDb rating gate → qBittorrent
- [`trakt-movies-qbittorrent.star`](trakt-movies-qbittorrent.star) — Trakt watchlist + ratings → two pipelines → qBittorrent
- [`movies-3d-qbittorrent.star`](movies-3d-qbittorrent.star) — 3D and flat pipelines side-by-side → qBittorrent
- [`movies-trakt-source.star`](movies-trakt-source.star) — `trakt_list` as standalone source → movies filter → qBittorrent

### Active search

- [`discover-trakt-qbittorrent.star`](discover-trakt-qbittorrent.star) — Trakt sources feed `discover` → Jackett search → qBittorrent

### News / articles

- [`ars-technica-email.star`](ars-technica-email.star) — RSS → keyword filter → email
- [`news-fanout.star`](news-fanout.star) — two RSS feeds merged → **fan-out** to email + persistent list

### Filesystem

- [`filesystem-cleanup.star`](filesystem-cleanup.star) — scan for *.part files → delete via exec
- [`filesystem-example.star`](filesystem-example.star) — scan .torrent files → reject spam → tag → print

### Routing and branching

- [`route-tv-movies.star`](route-tv-movies.star) — single feed → **route()**: TV → Transmission, movies → qBittorrent
- [`multi-client.star`](multi-client.star) — same split using parallel processor branches (no route)

### Quality management


### Show discovery

- [`premiere-new-shows.star`](premiere-new-shows.star) — **premiere** plugin: auto-download S01E01 of new series

### Notifications

- [`notify-webhook.star`](notify-webhook.star) — download episodes + send a **webhook** summary (Discord, Slack, Gotify)

### Advanced

- [`advanced-tv-pipeline.star`](advanced-tv-pipeline.star) — Trakt list + TVDB enrichment + condition + range-based quality ceiling + dedup + pathfmt + notify
