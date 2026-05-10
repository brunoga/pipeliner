# Configuration

Pipeliner uses a single Starlark file (default `config.star`). Pass a custom path with `--config`.

## Top-level structure

```python
# variables — assign values at the top of the file
tv_root = "/media/tv"

# templates — define reusable functions
def common_input(feed_url):
    return [plugin("rss", url=feed_url), plugin("seen")]

# tasks — one or more named pipelines
task("task-name", [
    plugin("plugin_name", option="value"),
] + common_input("https://example.com/rss"))

# schedules are set via the schedule= argument on task()
task("task-name", [...], schedule="1h")          # interval
task("task-name", [...], schedule="0 * * * *")   # cron expression
```

## Database

Pipeliner automatically maintains a single SQLite database named `pipeliner.db` in the same directory as the config file. All stateful plugins (`seen`, `series`, `movies`, `upgrade`, `premiere`, `discover`, the Trakt/TVDB caches, and the cross-task lists) share this one file. No configuration is needed — the file is created on first run and grows as state accumulates.

## Variables

Variables are ordinary Starlark assignments at the top of the file. They are useful for secrets and paths shared across multiple tasks.

```python
tv_root = "/media/tv"

task("my-task", [
    plugin("seen"),
    plugin("pathfmt", path=tv_root + "/{title}", field="download_path"),
])
```

## Templates

Templates are Starlark functions that return a list of plugin calls. Declare parameters normally and use them inside the function body.

Three call forms are supported (all just normal Starlark function calls):

```python
# Zero params — just call the function
common_input()

# Positional params — concise for short scalar values
common_input("https://example.com/rss", "localhost")

# Named params — clear for multiline values, list values, or many arguments
common_input(feed_url="https://example.com/rss", host="localhost")
```

Parameters can be any Starlark type: strings, numbers, lists, or multiline strings.

```python
def common_input(feed_url):
    return [
        plugin("rss", url=feed_url),
        plugin("seen"),
    ]

def common_output(dest, host):
    return [
        plugin("pathfmt", path=dest + "/{title}", field="download_path"),
        plugin("transmission", host=host, path="{download_path}"),
    ]

def jackett_search(indexers, categories):
    return [
        plugin("jackett_input", indexers=indexers, categories=categories),
    ]

def email_notify(subject, body_template):
    return [
        plugin("email", subject=subject, body_template=body_template),
    ]

task("tv-shows",
    common_input("https://example.com/rss/tv") +
    [plugin("series", static=["Breaking Bad"])] +
    common_output("/media/tv", "localhost") +
    jackett_search(["torrenting", "showrss"], ["5000"]) +
    email_notify(
        subject="New episodes: {{len .Entries}}",
        body_template="""{{range .Entries}}<p>{{index .Fields "title"}}</p>{{end}}""",
    )
)
```

## Tasks

A task is an ordered chain of plugins. Phases always execute in this fixed order; within each phase, plugins run in the order they appear in the list:

1. **input** — produces entries
2. **filter** — accepts, rejects, or leaves entries undecided
3. **metainfo** — annotates accepted entries with extra fields
4. **modify** — mutates entry fields
5. **output / notify** — acts on accepted entries

Each list item is a `plugin(name, ...)` call. If a plugin takes no config, call it with just the name.

```python
task("example", [
    plugin("rss", url="https://feeds.example.com/torrents"),
    plugin("seen"),
    plugin("series", static=["My Show"]),
    plugin("metainfo_quality"),
    plugin("pathfmt", path="/media/tv/{title}/Season {series_season:02d}", field="download_path"),
    plugin("transmission", host="localhost", port=9091),
])
```

## Schedules

In `daemon` mode, tasks run on the schedule defined in the `schedule=` argument of `task()`. Intervals (`1h`, `30m`, `24h`) and standard 5-field cron expressions are both supported.

```python
task("daily-task", [...], schedule="24h")
task("hourly-task", [...], schedule="1h")
task("cron-task", [...], schedule="0 6 * * *")   # daily at 06:00
```

Tasks without a `schedule=` argument are not run automatically by the daemon.

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

Custom fields can be set with the [`set`](../plugins/modify/set/README.md) plugin and read in any pattern expression.

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

## Example configs

See the other files in this directory for complete working examples:

- [`tv-series-deluge.star`](tv-series-deluge.star) — explicit show list → Deluge
- [`movie-downloads.star`](movie-downloads.star) — explicit movie list + rating gate → qBittorrent
- [`trakt-shows-transmission.star`](trakt-shows-transmission.star) — Trakt watchlist via `series.from` → Transmission
- [`trakt-movies-qbittorrent.star`](trakt-movies-qbittorrent.star) — Trakt watchlist via `movies.from` → qBittorrent
- [`tvdb-favorites-deluge.star`](tvdb-favorites-deluge.star) — TheTVDB favorites via `series.from` → Deluge
- [`discover-trakt-qbittorrent.star`](discover-trakt-qbittorrent.star) — active search driven by Trakt via `discover.from` → qBittorrent
- [`ars-technica-email.star`](ars-technica-email.star) — RSS → keyword filter → email
- [`filesystem-cleanup.star`](filesystem-cleanup.star) — filesystem entries → exec
