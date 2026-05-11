# Configuration

Pipeliner uses a single Starlark file (default `config.star`). Pass a custom path with `--config`.

## Top-level structure

```python
# variables — ordinary Starlark assignments
tv_root = "/media/tv"
tvdb_key = env("TVDB_API_KEY")   # read from environment

# templates — Starlark functions that build pipeline chains
def enrich_and_format(upstream, dest):
    meta = process("metainfo_tvdb", from_=upstream, api_key=tvdb_key)
    req  = process("require",       from_=meta, fields=["enriched"])
    fmt  = process("pathfmt",       from_=req,
                   path=dest + "/{title}/Season {series_season:02d}",
                   field="download_path")
    return fmt

# pipelines — connect nodes, call pipeline() to register
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",   from_=src)
series = process("series", from_=seen, static=["My Show"])
output("transmission", from_=enrich_and_format(series, tv_root),
       host="localhost", path="{download_path}")
pipeline("my-pipeline", schedule="1h")          # interval
# pipeline("cron-task",  schedule="0 6 * * *")  # cron expression
```

## Database

Pipeliner automatically maintains a single SQLite database named `pipeliner.db` in the same directory as the config file. All stateful plugins (`seen`, `series`, `movies`, `upgrade`, `premiere`, `discover`, the Trakt/TVDB caches, and the cross-task lists) share this one file. No configuration is needed — the file is created on first run and grows as state accumulates.

## Variables

Variables are ordinary Starlark assignments at the top of the file. They are useful for secrets and paths shared across multiple pipelines.

```python
tv_root = "/media/tv"

src = input("rss", url="https://example.com/rss")
fmt = process("pathfmt", from_=src, path=tv_root + "/{title}", field="download_path")
output("transmission", from_=fmt, host="localhost")
pipeline("my-pipeline")
```

## Templates

Templates are Starlark functions that accept an upstream node handle and return a downstream node handle. They can be composed by passing one function's return value into another.

```python
def common_source(feed_url, local_seen=False):
    src  = input("rss", url=feed_url)
    seen = process("seen", from_=src, local=local_seen)
    return seen

def common_sink(upstream, dest, host="localhost"):
    fmt = process("pathfmt", from_=upstream,
                  path=dest + "/{title}", field="download_path")
    output("transmission", from_=fmt, host=host, path="{download_path}")

# Compose templates
common_sink(
    process("series", from_=common_source("https://example.com/rss/tv"),
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

Entries carry a title, a URL, a state (undecided/accepted/rejected), and an arbitrary field map. Plugins read and write fields using the `{field}` pattern syntax (see [Pattern syntax](#pattern-syntax) below).

Fields follow a tiered naming convention. Three universal fields have no prefix; all others are prefixed by their info type.

### Universal (GenericInfo)

| Field | Set by | Description |
|-------|--------|-------------|
| `title` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt`, `metainfo_series`, `movies` | Canonical enriched display name |
| `description` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt` | Synopsis / overview |
| `published_date` | `metainfo_tvdb`, `metainfo_tmdb`, `input/rss` | Release or premiere date |
| `enriched` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt` | `true` when an external provider enriched this entry |
| `raw_title` | all inputs | Original entry title (torrent filename or feed item title) |

### Video (VideoInfo) — movies and series

| Field | Set by | Description |
|-------|--------|-------------|
| `video_year` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt`, `movies` | Release or premiere year |
| `video_language` | `metainfo_tvdb` | Original language (e.g. `English`) |
| `video_country` | `metainfo_tvdb` | Country of origin (e.g. `United States`) |
| `video_genres` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt` | Genre list |
| `video_rating` | `metainfo_tvdb`, `metainfo_tmdb`, `metainfo_trakt` | Audience rating (0–10) |
| `video_poster` | `metainfo_tvdb` | Poster image URL |
| `video_cast` | `metainfo_tvdb` | Cast list |
| `video_runtime` | `metainfo_tvdb`, `metainfo_tmdb` | Runtime in minutes |
| `video_quality` | `metainfo_quality`, `movies` | Full quality string including 3D format when present (e.g. `BD3D 1080p BluRay H.265`) |
| `video_resolution` | `metainfo_quality` | Resolution (e.g. `1080p`, `720p`) |
| `video_source` | `metainfo_quality` | Source (e.g. `BluRay`, `WEB-DL`, `HDTV`) |
| `video_is_3d` | `movies` | `true` when any 3D format marker is detected (3D, SBS, HOU, BD3D, etc.) |
| `video_imdb_id` | `metainfo_tmdb`, `metainfo_trakt` | IMDb ID (e.g. `tt1375666`) |

### Series (SeriesInfo)

| Field | Set by | Description |
|-------|--------|-------------|
| `series_season` | `series`, `metainfo_series` | Season number |
| `series_episode` | `series`, `metainfo_series` | Episode number |
| `series_episode_id` | `series`, `metainfo_series` | Episode ID string (e.g. `S02E05`) |
| `series_network` | `metainfo_tvdb` | Broadcasting network |
| `series_status` | `metainfo_tvdb` | Series status (e.g. `Ended`, `Continuing`) |
| `series_first_air_date` | `metainfo_tvdb` | Series premiere date (`time.Time`) |
| `series_episode_title` | `metainfo_tvdb` | Episode title |
| `series_episode_air_date` | `metainfo_tvdb` | Episode air date (`time.Time`) |
| `series_service` | `metainfo_series` | Streaming service tag from filename (e.g. `AMZN`, `NF`) |
| `series_proper` | `metainfo_series` | `true` for PROPER releases |
| `series_repack` | `metainfo_series` | `true` for REPACK releases |

### Movie (MovieInfo)

| Field | Set by | Description |
|-------|--------|-------------|
| `movie_tagline` | `metainfo_tmdb` | Movie tagline |

### Torrent (TorrentInfo)

| Field | Set by | Description |
|-------|--------|-------------|
| `torrent_link_type` | `jackett`, `jackett_input` | `"torrent"` or `"magnet"` — used by `metainfo_torrent`, `metainfo_magnet`, and `deluge` to route handling without a URL fetch |
| `torrent_info_hash` | `metainfo_torrent`, `metainfo_magnet`, `torrent_alive` | SHA-1 info hash (hex) |
| `torrent_file_size` | `metainfo_torrent`, `metainfo_magnet`, `jackett_input` | Total size in bytes |
| `torrent_file_count` | `metainfo_torrent`, `metainfo_magnet` | Number of files |
| `torrent_files` | `metainfo_torrent`, `metainfo_magnet` | List of file paths |
| `torrent_seeds` | `torrent_alive`, `input/rss`, `jackett_input` | Seed count |
| `torrent_leechers` | `jackett_input` | Leecher count |
| `torrent_announce` | `metainfo_torrent`, `metainfo_magnet` | Primary tracker URL |

### File (FileInfo)

| Field | Set by | Description |
|-------|--------|-------------|
| `file_name` | `filesystem` | Filename (without directory) |
| `file_extension` | `filesystem` | File extension including dot (e.g. `.torrent`) |
| `file_location` | `filesystem` | Absolute path on disk |
| `file_size` | `filesystem` | File size in bytes |
| `file_modified_time` | `filesystem` | Last-modified time |

### RSS (RSSInfo)

| Field | Set by | Description |
|-------|--------|-------------|
| `rss_feed` | `input/rss` | Feed URL |
| `rss_guid` | `input/rss` | Feed item GUID |
| `rss_link` | `input/rss` | Article/item link URL |

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

Custom fields can be set with the [`set`](../plugins/processor/modify/set/README.md) plugin and read in any pattern expression.

## Pattern syntax

String values in `pathfmt`, `exec`, `print`, `set`, and download-client path configs support a simple interpolation syntax:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `{field}` | Insert field value | `{title}` |
| `{field:format}` | Printf-formatted field | `{series_season:02d}` |

Go template syntax (`{{.field}}`) is still accepted for backward compatibility and is required for complex expressions like pipe chains (`{{.date \| slice 0 4}}`).

## Condition expressions

The `condition` plugin's `accept` and `reject` values use infix boolean syntax:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `field > value` | Numeric comparison | `video_rating > 7.0` |
| `field == "str"` | String equality | `video_source == "CAM"` |
| `field contains "str"` | Substring or slice contains | `video_genres contains "Documentary"` |
| `field matches "regex"` | Regexp match | `title matches "\\d{4}"` |
| `expr and expr` | Logical AND (`&&` also works) | `video_rating > 7.0 and video_source != "CAM"` |
| `expr or expr` | Logical OR (`\|\|` also works) | `video_source == "CAM" or video_source == "TS"` |
| `not expr` | Logical NOT (`!` also works) | `not video_source == "CAM"` |

Go template syntax (`{{gt .field value}}`) is still accepted for backward compatibility.

## Pipelines

Pipelines use `input()`, `process()`, `merge()`, `output()`, and `pipeline()` to wire plugins into a directed graph.

```python
src     = input("rss", url="https://example.com/rss")
quality = process("metainfo_quality", from_=src)
flt     = process("quality", from_=quality, min="720p")
output("transmission", from_=flt, host="localhost")
pipeline("my-pipeline", schedule="1h")
```

| Feature | How |
|---------|-----|
| Multiple RSS sources | `merge(src1, src2)` |
| Fan-out to N sinks | Reference the same node in multiple `output()` calls |
| Routing (TV→client A, movies→client B) | Two branches from one shared upstream node |
| Topology | Explicit — visible directly in the config |

## Example configs

### TV / series

- [`tv-series-deluge.star`](tv-series-deluge.star) — static show list → series filter → Deluge
- [`trakt-shows-transmission.star`](trakt-shows-transmission.star) — Trakt watchlist + trending → two pipelines → Transmission
- [`tvdb-favorites-deluge.star`](tvdb-favorites-deluge.star) — TheTVDB favorites → series filter → TVDB enrichment → Deluge
- [`jackett-tv-transmission.star`](jackett-tv-transmission.star) — active Jackett search driven by Trakt watchlist → Transmission
- [`dag-tv-two-feeds.star`](dag-tv-two-feeds.star) — **merge** two RSS feeds → dedup → series → Transmission

### Movies

- [`movie-downloads.star`](movie-downloads.star) — static list + TMDb rating gate → qBittorrent
- [`trakt-movies-qbittorrent.star`](trakt-movies-qbittorrent.star) — Trakt watchlist + ratings → two pipelines → qBittorrent
- [`movies-3d-qbittorrent.star`](movies-3d-qbittorrent.star) — 3D and flat pipelines side-by-side → qBittorrent
- [`dag-movies-trakt-source.star`](dag-movies-trakt-source.star) — `trakt_list` as standalone source → movies filter → qBittorrent

### Active search

- [`discover-trakt-qbittorrent.star`](discover-trakt-qbittorrent.star) — Trakt sources feed `discover` → Jackett search → qBittorrent

### News / articles

- [`ars-technica-email.star`](ars-technica-email.star) — RSS → keyword filter → email
- [`dag-news-fanout.star`](dag-news-fanout.star) — two RSS feeds merged → **fan-out** to email + persistent list

### Filesystem

- [`filesystem-cleanup.star`](filesystem-cleanup.star) — scan for *.part files → delete via exec
- [`filesystem-example.star`](filesystem-example.star) — scan .torrent files → reject spam → tag → print

### Multi-sink patterns

- [`dag-multi-client.star`](dag-multi-client.star) — single feed → **branch**: TV → Transmission, movies → qBittorrent
