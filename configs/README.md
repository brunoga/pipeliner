# Configuration

Pipeliner uses a single YAML file (default `config.yaml`). Pass a custom path with `--config`.

## Top-level structure

```yaml
variables:    # key-value substitutions applied before parsing
  key: value

templates:    # named partial task configs that tasks can inherit
  name:
    plugin_name:
      option: value

tasks:        # one or more named pipelines
  task-name:
    plugin_name:
      option: value

schedules:    # when to run each task in daemon mode
  task-name: 1h          # interval
  task-name: "0 * * * *" # cron expression
```

## Database

Pipeliner automatically maintains a single SQLite database named `pipeliner.db` in the same directory as the config file. All stateful plugins (`seen`, `series`, `movies`, `upgrade`, `premiere`, `discover`, the Trakt/TVDB caches, and the cross-task lists) share this one file. No configuration is needed — the file is created on first run and grows as state accumulates.

## Variables

Variables are substituted using `{$ key $}` anywhere in the file before YAML is parsed. They are useful for secrets and paths shared across multiple tasks.

```yaml
variables:
  tv_root: /media/tv

tasks:
  my-task:
    seen:
    pathfmt:
      path: "{$ tv_root $}/{series_name}"
```

## Templates

Templates let you define common plugin config once and inherit it in multiple tasks. A task's `template:` key names the template to merge from; per-task plugin config is merged on top and wins.

```yaml
templates:
  base:
    rss:
      url: "https://example.com/rss"
    seen:

tasks:
  task-a:
    template: base
    series:
      shows: ["Breaking Bad"]
    transmission:
      host: localhost

  task-b:
    template: base       # same rss + seen config
    movies:
      movies: ["Inception"]
    deluge:
      host: localhost
```

## Tasks

A task is an ordered chain of plugins. Phases always execute in this fixed order; within each phase, plugins run in the order they appear in the YAML:

1. **input** — produces entries
2. **filter** — accepts, rejects, or leaves entries undecided
3. **metainfo** — annotates accepted entries with extra fields
4. **modify** — mutates entry fields
5. **output / notify** — acts on accepted entries

Each plugin key maps to that plugin's config map. If a plugin takes no config, the value can be `null` or an empty map.

```yaml
tasks:
  example:
    rss:
      url: "https://feeds.example.com/torrents"
    seen:
    series:
      shows:
        - "My Show"
    metainfo_quality:    # no config needed
    pathfmt:
      path: "/media/tv/{series_name}/Season {series_season:02d}"
    transmission:
      host: localhost
      port: 9091
```

## Schedules

In `daemon` mode, tasks run on the schedule defined here. Intervals (`1h`, `30m`, `24h`) and standard 5-field cron expressions are both supported.

```yaml
schedules:
  daily-task: 24h
  hourly-task: 1h
  cron-task: "0 6 * * *"   # daily at 06:00
```

Tasks without a schedule entry are not run automatically by the daemon.

## Entry fields

Entries carry a title, a URL, a state (undecided/accepted/rejected), and an arbitrary string→any field map. Plugins read and write fields using the `{field}` pattern syntax (see [Pattern syntax](#pattern-syntax) below).

| Field | Set by | Description |
|-------|--------|-------------|
| `series_name` | `series`, `metainfo_series` | Canonical show name |
| `series_season` | `series`, `metainfo_series` | Season number |
| `series_episode` | `series`, `metainfo_series` | Episode number |
| `series_episode_id` | `series`, `metainfo_series` | Episode ID string, e.g. `S02E05` |
| `movie_title` | `movies` | Canonical movie title |
| `movie_year` | `movies` | Movie release year |
| `resolution` | `metainfo_quality` | Resolution tag (e.g. `1080p`) |
| `source` | `metainfo_quality` | Source tag (e.g. `BluRay`, `HDTV`) |
| `codec` | `metainfo_quality` | Codec tag (e.g. `x264`, `HEVC`) |
| `download_path` | `pathfmt` | Rendered download directory path |
| `tmdb_id` | `metainfo_tmdb` | TMDb movie/series ID |
| `tmdb_title` | `metainfo_tmdb` | TMDb canonical title |
| `tmdb_vote_average` | `metainfo_tmdb` | TMDb vote average (0–10) |
| `tmdb_genres` | `metainfo_tmdb` | List of genre names |
| `tmdb_release_date` | `metainfo_tmdb` | Release date string (ISO 8601) |
| `tvdb_id` | `metainfo_tvdb`, `tvdb_favorites` | TheTVDB series ID |
| `tvdb_series_name` | `metainfo_tvdb` | TheTVDB canonical series name |
| `tvdb_first_aired` | `metainfo_tvdb` | First air date string |
| `tvdb_genres` | `metainfo_tvdb` | List of genre names |
| `trakt_id` | `metainfo_trakt`, `trakt_list` | Trakt internal ID |
| `trakt_rating` | `metainfo_trakt` | Trakt community rating |
| `trakt_genres` | `metainfo_trakt` | List of genre names |
| `trakt_year` | `trakt_list` | Release or premiere year |
| `trakt_imdb_id` | `trakt_list` | IMDb ID (e.g. `tt1375666`) |
| `trakt_tmdb_id` | `trakt_list` | TMDb ID |
| `torrent_info_hash` | `metainfo_torrent`, `metainfo_magnet` | SHA-1 info hash (hex) |
| `torrent_name` | `metainfo_torrent`, `metainfo_magnet` | Torrent name |
| `torrent_size` | `metainfo_torrent`, `metainfo_magnet` | Total size in bytes |
| `torrent_file_count` | `metainfo_torrent`, `metainfo_magnet` | Number of files |
| `torrent_files` | `metainfo_torrent`, `metainfo_magnet` | List of file paths |
| `torrent_announce` | `metainfo_torrent`, `metainfo_magnet` | Primary tracker URL |
| `torrent_announce_list` | `metainfo_magnet` | All tracker announce URLs |
| `torrent_display_name` | `metainfo_magnet` | Display name from magnet URI `dn=` param |
| `torrent_private` | `metainfo_torrent` | Whether the torrent is private |
| `location` | `filesystem` | Absolute file path on disk |

Custom fields can be set with the [`set`](../plugins/modify/set/README.md) plugin and read in any pattern expression.

## Pattern syntax

String values in `pathfmt`, `exec`, `print`, `set`, and download-client path configs support a simple interpolation syntax:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `{field}` | Insert field value | `{series_name}` |
| `{field:format}` | Printf-formatted field | `{series_season:02d}` |

Go template syntax (`{{.field}}`) is still accepted for backward compatibility and is required for complex expressions like pipe chains (`{{.date \| slice 0 4}}`).

## Condition expressions

The `condition` plugin's `accept` and `reject` values use infix boolean syntax:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `field > value` | Numeric comparison | `tmdb_vote_average > 7.0` |
| `field == "str"` | String equality | `source == "CAM"` |
| `field contains "str"` | Substring match | `genre contains "documentary"` |
| `field matches "regex"` | Regexp match | `title matches "\\d{4}"` |
| `expr and expr` | Logical AND (`&&` also works) | `score > 7.0 and source != "CAM"` |
| `expr or expr` | Logical OR (`\|\|` also works) | `source == "CAM" or source == "TS"` |
| `not expr` | Logical NOT (`!` also works) | `not source == "CAM"` |

Go template syntax (`{{gt .field value}}`) is still accepted for backward compatibility.

## Example configs

See the other files in this directory for complete working examples:

- [`tv-series-deluge.yaml`](tv-series-deluge.yaml) — explicit show list → Deluge
- [`movie-downloads.yaml`](movie-downloads.yaml) — explicit movie list + TMDb rating gate → qBittorrent
- [`trakt-shows-transmission.yaml`](trakt-shows-transmission.yaml) — Trakt watchlist via `series.from` → Transmission
- [`trakt-movies-qbittorrent.yaml`](trakt-movies-qbittorrent.yaml) — Trakt watchlist via `movies.from` → qBittorrent
- [`tvdb-favorites-deluge.yaml`](tvdb-favorites-deluge.yaml) — TheTVDB favorites via `series.from` → Deluge
- [`discover-trakt-qbittorrent.yaml`](discover-trakt-qbittorrent.yaml) — active search driven by Trakt via `discover.from` → qBittorrent
- [`ars-technica-email.yaml`](ars-technica-email.yaml) — RSS → keyword filter → email
- [`filesystem-cleanup.yaml`](filesystem-cleanup.yaml) — filesystem entries → exec
