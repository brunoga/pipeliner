# pipeliner

A media-automation tool that pulls entries from RSS feeds, active searches, or local filesystems, filters them against configurable rules, enriches them with metadata, and hands them off to download clients, notification services, or arbitrary shell commands.

Heavily inspired by [FlexGet](https://flexget.com). Pipelines are described in YAML. Each pipeline task chains a sequence of plugins — one input, any number of filters and metainfo annotators, optional modifiers, and one or more outputs. The scheduler runs tasks on cron or interval schedules.

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

```yaml
# config.yaml
tasks:
  breaking-bad:
    rss:
      url: "https://example.com/rss"
    seen:
    series:
      shows:
        - "Breaking Bad"
    transmission:
      host: localhost
      port: 9091

schedules:
  breaking-bad: 1h
```

```sh
pipeliner run               # run all tasks once
pipeliner daemon            # run tasks on their schedules
pipeliner check             # validate config without running
pipeliner list-plugins      # print all registered plugins
```

## Configuration

See [`configs/`](configs/README.md) for the full config format reference and annotated examples.

## Plugins

Pipeliner is built entirely from plugins. Each task is a chain of plugins executed in phase order:

| Phase | Purpose |
|-------|---------|
| **input** | Produce entries from sources (RSS, files, searches) |
| **metainfo** | Annotate entries with metadata (quality, series info, TMDb) |
| **filter** | Accept, reject, or leave entries undecided based on rules |
| **modify** | Mutate entry fields (path formatting, field setting) |
| **output** | Act on accepted entries (download, client RPC, notify) |
| **learn** | Persist decisions and state for future runs (seen, series tracking) |

See [`plugins/`](plugins/README.md) for the plugin model and links to every plugin.

### Input

| Plugin | Description |
|--------|-------------|
| [`rss`](plugins/input/rss/README.md) | Fetch entries from an RSS/Atom feed |
| [`html`](plugins/input/html/README.md) | Scrape entries from an HTML page with CSS selectors |
| [`filesystem`](plugins/input/filesystem/README.md) | Walk a directory tree and emit file entries |
| [`discover`](plugins/input/discover/README.md) | Actively search multiple sources for a configured title list |
| [`input_trakt`](plugins/input/trakt/README.md) | Fetch movies or shows from a Trakt.tv list |
| [`input_tvdb`](plugins/input/tvdb/README.md) | Fetch shows from a TheTVDB user's favorites list |

### Filter

| Plugin | Description |
|--------|-------------|
| [`seen`](plugins/filter/seen/README.md) | Reject entries already processed in a previous run |
| [`series`](plugins/filter/series/README.md) | Accept episodes of configured TV shows; track downloads |
| [`movies`](plugins/filter/movies/README.md) | Accept movies from a configured title list; track downloads |
| [`list_match`](plugins/filter/list_match/README.md) | Accept entries whose title is in a persistent cross-task list |
| [`trakt`](plugins/filter/trakt/README.md) | Accept entries whose title matches a Trakt.tv list |
| [`tvdb`](plugins/filter/tvdb/README.md) | Accept entries whose title matches TheTVDB user favorites |
| [`quality`](plugins/filter/quality/README.md) | Reject entries below or above a quality range |
| [`regexp`](plugins/filter/regexp/README.md) | Accept or reject entries by regular expression |
| [`exists`](plugins/filter/exists/README.md) | Reject entries whose target file already exists on disk |
| [`condition`](plugins/filter/condition/README.md) | Accept or reject entries using boolean expressions |
| [`content`](plugins/filter/content/README.md) | Reject or require entries based on torrent file contents |
| [`premiere`](plugins/filter/premiere/README.md) | Reject entries for episodes that have not yet aired |
| [`torrentalive`](plugins/filter/torrentalive/README.md) | Reject torrents with no active seeders |
| [`upgrade`](plugins/filter/upgrade/README.md) | Accept entries that are a quality upgrade over what is on disk |
| [`require`](plugins/filter/require/README.md) | Reject entries missing one or more required fields |
| [`accept_all`](plugins/filter/accept_all/README.md) | Accept every undecided entry unconditionally |

### Metainfo

| Plugin | Description |
|--------|-------------|
| [`metainfo_quality`](plugins/metainfo/quality/README.md) | Parse quality tags (resolution, source, codec) from the title |
| [`metainfo_series`](plugins/metainfo/series/README.md) | Parse series name, season, and episode from the title |
| [`metainfo_tmdb`](plugins/metainfo/tmdb/README.md) | Enrich movie entries with TMDb metadata |
| [`metainfo_tvdb`](plugins/metainfo/tvdb/README.md) | Enrich series entries with TheTVDB metadata |
| [`metainfo_trakt`](plugins/metainfo/trakt/README.md) | Annotate entries with Trakt.tv metadata |
| [`metainfo_torrent`](plugins/metainfo/torrent/README.md) | Read `.torrent` file metadata (info hash, size, file list) |
| [`metainfo_magnet`](plugins/metainfo/magnet/README.md) | Annotate magnet-link entries with info hash, trackers, and DHT metadata |

### Modify

| Plugin | Description |
|--------|-------------|
| [`pathfmt`](plugins/modify/pathfmt/README.md) | Render a pattern into the `download_path` field |
| [`pathscrub`](plugins/modify/pathscrub/README.md) | Sanitize path components for safe filesystem use |
| [`set`](plugins/modify/set/README.md) | Unconditionally set one or more entry fields |

### Output

| Plugin | Description |
|--------|-------------|
| [`transmission`](plugins/output/transmission/README.md) | Add torrents to a Transmission client via JSON-RPC |
| [`deluge`](plugins/output/deluge/README.md) | Add torrents to a Deluge client via JSON-RPC |
| [`qbittorrent`](plugins/output/qbittorrent/README.md) | Add torrents to a qBittorrent client via Web API |
| [`download`](plugins/output/download/README.md) | Download the entry URL to a local file |
| [`exec`](plugins/output/exec/README.md) | Run a shell command for each accepted entry |
| [`decompress`](plugins/output/decompress/README.md) | Decompress downloaded archives (zip, rar, tar.gz, …) |
| [`list_add`](plugins/output/list_add/README.md) | Add accepted entries to a named persistent list |
| [`email`](plugins/output/email/README.md) | Send an email for each accepted entry |
| [`print`](plugins/output/print/README.md) | Print accepted entries to stdout |
| [`notify`](plugins/output/notify/README.md) | Delegate to configured notify plugins |

### Notify Notifiers

| Plugin | Description |
|--------|-------------|
| [`email`](plugins/notify/email/README.md) | Send a run-summary email via SMTP |
| [`pushover`](plugins/notify/pushover/README.md) | Send a notification via the Pushover API |
| [`webhook`](plugins/notify/webhook/README.md) | POST a run summary to an HTTP endpoint |

## Example configs

| File | Description |
|------|-------------|
| [`configs/tv-series-deluge.yaml`](configs/tv-series-deluge.yaml) | TV shows by explicit list → Deluge |
| [`configs/movie-downloads.yaml`](configs/movie-downloads.yaml) | Movies by explicit list + TMDb rating gate → qBittorrent |
| [`configs/trakt-shows-transmission.yaml`](configs/trakt-shows-transmission.yaml) | TV shows via Trakt watchlist (`series.from`) → Transmission |
| [`configs/trakt-movies-qbittorrent.yaml`](configs/trakt-movies-qbittorrent.yaml) | Movies via Trakt watchlist (`movies.from`) → qBittorrent |
| [`configs/tvdb-favorites-deluge.yaml`](configs/tvdb-favorites-deluge.yaml) | TV shows via TheTVDB favorites (`series.from`) → Deluge |
| [`configs/discover-trakt-qbittorrent.yaml`](configs/discover-trakt-qbittorrent.yaml) | Active search driven by Trakt watchlist (`discover.from`) → qBittorrent |
| [`configs/ars-technica-email.yaml`](configs/ars-technica-email.yaml) | RSS articles filtered by keyword → email |
| [`configs/filesystem-cleanup.yaml`](configs/filesystem-cleanup.yaml) | File system entries → conditional exec |
