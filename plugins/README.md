# Plugins

Pipeliner is built from plugins connected into pipelines. Every plugin implements one of three roles:

| Role | Used as | Purpose |
|------|---------|---------|
| **source** | `input(…)` | Produce entries from RSS, files, indexers |
| **processor** | `process(…, upstream=…)` | Filter, enrich, or transform entries |
| **sink** | `output(…, upstream=…)` | Act on accepted entries (download, notify, exec) |

## Typical pipeline order

Most pipelines follow this order — you only need the stages relevant to your use case:

1. **Source** — `rss`, `filesystem`, `jackett`, `discover`, …
2. **Dedup across runs** — `seen` (reject entries processed in previous runs)
3. **Enrichment** — `metainfo_file` (parse everything from the filename in one pass); add `metainfo_tvdb` / `metainfo_tmdb` / `metainfo_trakt` for external metadata APIs
4. **Quality requirement check** — `require(fields=["enriched"])` to drop entries that couldn't be identified
5. **Content filter** — `series`, `movies`, `premiere`, `condition`, `regexp`, … (accept or reject)
6. **Within-run dedup** — `dedup` (keep the best copy when multiple variants arrive in one run)
7. **Field setup** — `pathfmt`, `set` (compute download paths or set custom fields)
8. **Sink** — `transmission`, `deluge`, `qbittorrent`, `exec`, `notify`, …

Not every stage is required. A simple news-to-email pipeline might just be `rss → seen → regexp → notify(via="email")`.

## Sources (`input(…)`)

| Plugin | Description |
|--------|-------------|
| [`rss`](source/rss/README.md) | Fetch entries from an RSS/Atom feed |
| [`html`](source/html/README.md) | Scrape entries from an HTML page |
| [`filesystem`](source/filesystem/README.md) | Walk a directory tree and emit file entries |
| [`jackett`](source/jackett/README.md) | Fetch recent results from Jackett indexers |
| [`trakt_list`](source/trakt_list/README.md) | Fetch movies or shows from a Trakt.tv list |
| [`tvdb_favorites`](source/tvdb_favorites/README.md) | Fetch shows from a TheTVDB user's favorites |
| [`bluray_releases`](source/bluray_releases/README.md) | Scrape the Blu-ray.com release calendar |
| [`jackett`](source/jackett/README.md) (search mode) | Also acts as a search backend for `discover` via `url_template` |
| [`rss`](source/rss/README.md) (search mode) | Also acts as a search backend for `discover` via `url_template` |
| [`bluray_releases`](source/bluray_releases/README.md) (search mode) | Also acts as a search backend for `discover` |

## Processors — filtering

| Plugin | Description |
|--------|-------------|
| [`seen`](processor/filter/seen/README.md) | Reject entries already processed in a previous run |
| [`series`](processor/filter/series/README.md) | Accept episodes of configured TV shows; track downloads |
| [`movies`](processor/filter/movies/README.md) | Accept movies from a configured title list; track downloads |
| [`list_match`](processor/filter/list_match/README.md) | Accept entries whose title is in a persistent cross-task list |
| [`age`](processor/filter/age/README.md) | Reject entries whose date field falls outside a configured age window |
| [`regexp`](processor/filter/regexp/README.md) | Accept or reject entries by regular expression |
| [`exists`](processor/filter/exists/README.md) | Reject entries whose target file already exists on disk |
| [`condition`](processor/filter/condition/README.md) | Accept or reject entries using boolean expressions |
| [`route`](processor/filter/route/README.md) | Route entries to named mutually-exclusive ports based on ordered boolean conditions |
| [`content`](processor/filter/content/README.md) | Reject or require entries based on torrent file contents |
| [`premiere`](processor/filter/premiere/README.md) | Accept the first episode of previously unseen series (automatic show discovery) |
| [`torrentalive`](processor/filter/torrentalive/README.md) | Reject torrents with no active seeders |
| [`trailer`](processor/filter/trailer/README.md) | Reject trailer/teaser/featurette/BTS entries (or keep only them) |
| [`require`](processor/filter/require/README.md) | Reject entries missing one or more required fields |
| [`dedup`](processor/filter/dedup/README.md) | Keep the best-quality copy when multiple entries refer to the same episode or movie |
| [`accept_all`](processor/filter/accept_all/README.md) | Accept every undecided entry unconditionally |

## Processors — active search

| Plugin | Description |
|--------|-------------|
| [`discover`](processor/discover/README.md) | Search multiple backends for entries matching a title list; a per-title cooldown avoids redundant queries |

## Processors — enrichment

| Plugin | Description |
|--------|-------------|
| [`metainfo_file`](processor/metainfo/file/README.md) | Parse the filename and annotate everything detectable in one pass: classify as series/movie, set series/movie/quality fields, and stamp `media_type` |
| [`metainfo_tmdb`](processor/metainfo/tmdb/README.md) | Enrich movie entries with TMDb metadata |
| [`metainfo_tvdb`](processor/metainfo/tvdb/README.md) | Enrich series entries with TheTVDB metadata |
| [`metainfo_trakt`](processor/metainfo/trakt/README.md) | Annotate entries with Trakt.tv metadata |
| [`metainfo_bluray`](processor/metainfo/bluray/README.md) | Enrich movie entries with Blu-ray.com metadata; sets `bluray_3d_release` to identify real 3D Blu-ray titles vs fake/upscaled 3D rips |
| [`metainfo_torrent`](processor/metainfo/torrent/README.md) | Read `.torrent` file metadata (info hash, size, file list) |
| [`metainfo_magnet`](processor/metainfo/magnet/README.md) | Annotate magnet-link entries with info hash, trackers, and DHT metadata |

## Processors — field modification

| Plugin | Description |
|--------|-------------|
| [`pathfmt`](processor/modify/pathfmt/README.md) | Render a path pattern into a named field, with automatic scrubbing |
| [`set`](processor/modify/set/README.md) | Unconditionally set one or more entry fields |

## Sinks (`output(…)`)

| Plugin | Description |
|--------|-------------|
| [`transmission`](sink/transmission/README.md) | Add torrents to a Transmission client via JSON-RPC |
| [`deluge`](sink/deluge/README.md) | Add torrents to a Deluge client via JSON-RPC |
| [`qbittorrent`](sink/qbittorrent/README.md) | Add torrents to a qBittorrent client via Web API |
| [`download`](sink/download/README.md) | Download the entry URL to a local file |
| [`exec`](sink/exec/README.md) | Run a shell command for each accepted entry |
| [`decompress`](sink/decompress/README.md) | Decompress downloaded archives (zip, rar, tar.gz, …) |
| [`list_add`](sink/list_add/README.md) | Add accepted entries to a named persistent list |
| [`print`](sink/print/README.md) | Print accepted entries to stdout |
| [`notify`](sink/notify/README.md) | Send a per-run batch notification via a configured backend (email, webhook, Pushover) |

## Notification backends (used with the `notify` sink)

The `notify` sink dispatches to one of these backends via `via="name"`.

| Backend | Description |
|---------|-------------|
| [`email`](sink/notify/email/README.md) | Send a run-summary email via SMTP |
| [`pushover`](sink/notify/pushover/README.md) | Send a push notification via the Pushover API |
| [`webhook`](sink/notify/webhook/README.md) | POST a run summary to an HTTP endpoint (Discord, Slack, Gotify, …) |

---

Writing a new plugin? See the [Plugin Development Guide](../PLUGIN_DEVELOPMENT_GUIDE.md).
