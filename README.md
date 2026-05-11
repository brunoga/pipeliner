# pipeliner

A media-automation tool that pulls entries from RSS feeds, active searches, or local filesystems, filters them against configurable rules, enriches them with metadata, and hands them off to download clients, notification services, or arbitrary shell commands.

Heavily inspired by [FlexGet](https://flexget.com). Pipelines are described in [Starlark](https://github.com/bazelbuild/starlark) — a deterministic Python dialect used by Bazel and Buck. Plugins are connected into pipelines: sources produce entries, processors filter and enrich them, sinks act on the accepted ones. The scheduler runs pipelines on cron or interval schedules.

## Installation

```sh
go install github.com/brunoga/pipeliner/cmd/pipeliner@latest
```

Or build from source:

```sh
git clone https://github.com/brunoga/pipeliner
cd pipeliner
go build -o pipeliner ./cmd/pipeliner
```

## Quick start

```python
# config.star — connect nodes with from_= to build a pipeline
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",       from_=src)
series = process("series",     from_=seen, static=["Breaking Bad"])
fmt    = process("pathfmt",    from_=series,
                 path="/media/tv/{title}/Season {series_season:02d}",
                 field="download_path")
output("transmission", from_=fmt, host="localhost", port=9091,
                        path="{download_path}")
pipeline("breaking-bad", schedule="1h")
```

```sh
pipeliner run               # run all pipelines once
pipeliner daemon            # run pipelines on their schedules
pipeliner check             # validate config without running
pipeliner list-plugins      # print all registered plugins
```

## Configuration

See [`configs/`](configs/README.md) for the full config format reference and annotated examples.

## Plugins

Pipeliner is built entirely from plugins. Each plugin has one of three roles:

| Role | Used as | Purpose |
|------|---------|---------|
| **source** | `input(…)` | Produce entries from RSS, files, indexers |
| **processor** | `process(…, from_=…)` | Filter, enrich, or transform entries |
| **sink** | `output(…, from_=…)` | Act on accepted entries (download, notify, exec) |

Connect multiple sources with `merge(src1, src2)` for deduplication. Fan out to multiple sinks by calling `output()` more than once from the same upstream node. See [`plugins/`](plugins/README.md) for the full plugin model.

The visual pipeline editor in the web UI lets you build pipelines by clicking and connecting nodes without writing config by hand.

### Sources (`input(…)`)

| Plugin | Description |
|--------|-------------|
| [`rss`](plugins/source/rss/README.md) | Fetch entries from an RSS/Atom feed |
| [`html`](plugins/source/html/README.md) | Scrape entries from an HTML page |
| [`filesystem`](plugins/source/filesystem/README.md) | Walk a directory tree and emit file entries |
| [`jackett_input`](plugins/source/jackett_input/README.md) | Fetch recent results from Jackett indexers |
| [`trakt_list`](plugins/source/trakt_list/README.md) | Fetch movies or shows from a Trakt.tv list |
| [`tvdb_favorites`](plugins/source/tvdb_favorites/README.md) | Fetch shows from a TheTVDB user's favorites |
| [`jackett`](plugins/source/jackett/README.md) | Search Jackett via Torznab (also a search backend for `discover.via`) |
| [`rss_search`](plugins/source/rss_search/README.md) | Search a parameterized RSS URL (also a backend for `discover.via`) |

### Processors — filtering

| Plugin | Description |
|--------|-------------|
| [`seen`](plugins/processor/filter/seen/README.md) | Reject entries already processed in a previous run |
| [`series`](plugins/processor/filter/series/README.md) | Accept episodes of configured TV shows; track downloads |
| [`movies`](plugins/processor/filter/movies/README.md) | Accept movies from a configured title list; track downloads |
| [`list_match`](plugins/processor/filter/list_match/README.md) | Accept entries whose title is in a persistent cross-task list |
| [`trakt`](plugins/processor/filter/trakt/README.md) | Accept entries whose title matches a Trakt.tv list |
| [`tvdb`](plugins/processor/filter/tvdb/README.md) | Accept entries whose title matches TheTVDB user favorites |
| [`quality`](plugins/processor/filter/quality/README.md) | Reject entries below or above a quality range |
| [`regexp`](plugins/processor/filter/regexp/README.md) | Accept or reject entries by regular expression |
| [`exists`](plugins/processor/filter/exists/README.md) | Reject entries whose target file already exists on disk |
| [`condition`](plugins/processor/filter/condition/README.md) | Accept or reject entries using boolean expressions |
| [`content`](plugins/processor/filter/content/README.md) | Reject or require entries based on torrent file contents |
| [`premiere`](plugins/processor/filter/premiere/README.md) | Reject entries for episodes that have not yet aired |
| [`torrentalive`](plugins/processor/filter/torrentalive/README.md) | Reject torrents with no active seeders |
| [`upgrade`](plugins/processor/filter/upgrade/README.md) | Accept entries that are a quality upgrade over what is on disk |
| [`require`](plugins/processor/filter/require/README.md) | Reject entries missing one or more required fields |
| [`accept_all`](plugins/processor/filter/accept_all/README.md) | Accept every undecided entry unconditionally |

### Processors — enrichment

| Plugin | Description |
|--------|-------------|
| [`metainfo_quality`](plugins/processor/metainfo/quality/README.md) | Parse quality tags (resolution, source, codec) from the title |
| [`metainfo_series`](plugins/processor/metainfo/series/README.md) | Parse series name, season, and episode from the title |
| [`metainfo_tmdb`](plugins/processor/metainfo/tmdb/README.md) | Enrich movie entries with TMDb metadata |
| [`metainfo_tvdb`](plugins/processor/metainfo/tvdb/README.md) | Enrich series entries with TheTVDB metadata |
| [`metainfo_trakt`](plugins/processor/metainfo/trakt/README.md) | Annotate entries with Trakt.tv metadata |
| [`metainfo_torrent`](plugins/processor/metainfo/torrent/README.md) | Read `.torrent` file metadata (info hash, size, file list) |
| [`metainfo_magnet`](plugins/processor/metainfo/magnet/README.md) | Annotate magnet-link entries with info hash, trackers, and DHT metadata |

### Processors — field mutation

| Plugin | Description |
|--------|-------------|
| [`pathfmt`](plugins/processor/modify/pathfmt/README.md) | Render a path pattern into a named field, with automatic scrubbing |
| [`set`](plugins/processor/modify/set/README.md) | Unconditionally set one or more entry fields |

### Sinks (`output(…)`)

| Plugin | Description |
|--------|-------------|
| [`transmission`](plugins/sink/transmission/README.md) | Add torrents to a Transmission client via JSON-RPC |
| [`deluge`](plugins/sink/deluge/README.md) | Add torrents to a Deluge client via JSON-RPC |
| [`qbittorrent`](plugins/sink/qbittorrent/README.md) | Add torrents to a qBittorrent client via Web API |
| [`download`](plugins/sink/download/README.md) | Download the entry URL to a local file |
| [`exec`](plugins/sink/exec/README.md) | Run a shell command for each accepted entry |
| [`decompress`](plugins/sink/decompress/README.md) | Decompress downloaded archives (zip, rar, tar.gz, …) |
| [`list_add`](plugins/sink/list_add/README.md) | Add accepted entries to a named persistent list |
| [`email`](plugins/sink/email/README.md) | Send an email for each accepted entry |
| [`print`](plugins/sink/print/README.md) | Print accepted entries to stdout |
| [`notify`](plugins/sink/notify/README.md) | Delegate to configured notify plugins |

### Sink notifiers (used by `notify`)

| Plugin | Description |
|--------|-------------|
| [`email`](plugins/sink/notify/email/README.md) | Send a run-summary email via SMTP |
| [`pushover`](plugins/sink/notify/pushover/README.md) | Send a notification via the Pushover API |
| [`webhook`](plugins/sink/notify/webhook/README.md) | POST a run summary to an HTTP endpoint |

## Standard fields

Every entry carries a `Fields` map that plugins read and write. Pipeliner defines a hierarchy of **standard fields** so conditions, pathfmt patterns, and templates work the same regardless of which metainfo provider is configured.

| Tier | Prefix | Example fields |
|------|--------|----------------|
| Generic — any entry | *(none)* | `title`, `description`, `published_date`, `enriched` |
| Video — movies and series | `video_` | `video_year`, `video_genres`, `video_rating`, `video_quality`, `video_language`, … |
| Movie-specific | `movie_` | `movie_tagline` |
| Series-specific | `series_` | `series_season`, `series_episode`, `series_episode_id`, `series_network`, `series_episode_title`, … |
| Torrent | `torrent_` | `torrent_seeds`, `torrent_info_hash`, `torrent_file_size`, … |
| File | `file_` | `file_name`, `file_location`, `file_size`, … |
| RSS | `rss_` | `rss_feed`, `rss_guid`, `rss_link`, … |

`title` is the canonical display name set by external metainfo providers (TVDB, TMDb, Trakt). The raw entry title as parsed from the filename or feed is available as `raw_title`.

`enriched` is set to `true` by any external metainfo provider on a successful lookup. Use it with [`require`](plugins/processor/filter/require/README.md) to discard entries that couldn't be identified:

```python
req = process("require", from_=meta_node, fields=["enriched"])
# works with TVDB, TMDb, or Trakt — no change needed if you swap providers
```

Provider-specific fields (e.g. `tvdb_id`, `tmdb_id`, `trakt_slug`) are still set alongside the standard fields for cases that need them.

## Platforms

Pre-built binaries are available for every [release](https://github.com/brunoga/pipeliner/releases):

| OS | Architectures |
|----|---------------|
| Linux | `amd64`, `arm64`, `arm/v7` |
| Windows | `amd64`, `arm64` |
| macOS | `amd64` (Intel), `arm64` (Apple Silicon) |

## Docker

Multi-platform Linux images are published to the GitHub Container Registry on every release tag:

```sh
docker pull ghcr.io/brunoga/pipeliner:latest
```

Supported platforms: `linux/amd64`, `linux/arm64`, `linux/arm/v7`.

```sh
docker run -d \
  -p 8080:8080 \
  -v pipeliner-data:/config \
  -e PIPELINER_WEB_USER=admin \
  -e PIPELINER_WEB_PASSWORD=secret \
  ghcr.io/brunoga/pipeliner:latest
```

| Environment variable | Default | Description |
|----------------------|---------|-------------|
| `PIPELINER_WEB_USER` | — | Web UI username **(required)** |
| `PIPELINER_WEB_PASSWORD` | — | Web UI password **(required)** |
| `PIPELINER_WEB_ADDR` | `:8080` | Listen address |
| `PIPELINER_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `PIPELINER_CONFIG` | `/config/config.star` | Config file path |

The `/config` volume holds both `config.star` and `pipeliner.db` (state database). Mount a named volume or bind-mount to persist across restarts. The config can be edited live through the web UI's **Edit Config** tab.

## Example configs

| File | Description |
|------|-------------|
| [`configs/tv-series-deluge\.star`](configs/\1.star) | TV shows by explicit list → Deluge |
| [`configs/movie-downloads\.star`](configs/\1.star) | Movies by explicit list + TMDb rating gate → qBittorrent |
| [`configs/trakt-shows-transmission\.star`](configs/\1.star) | TV shows via Trakt watchlist (`series.from`) → Transmission |
| [`configs/trakt-movies-qbittorrent\.star`](configs/\1.star) | Movies via Trakt watchlist (`movies.from`) → qBittorrent |
| [`configs/tvdb-favorites-deluge\.star`](configs/\1.star) | TV shows via TheTVDB favorites (`series.from`) → Deluge |
| [`configs/discover-trakt-qbittorrent\.star`](configs/\1.star) | Active search driven by Trakt watchlist (`discover.from`) → qBittorrent |
| [`configs/ars-technica-email\.star`](configs/\1.star) | RSS articles filtered by keyword → email |
| [`configs/filesystem-cleanup\.star`](configs/\1.star) | File system entries → conditional exec |
