# Source plugins (`plugins/source/`)

Source plugins produce entries. Use them with `input("plugin-name", …)` in a pipeline.
Combine multiple sources with `merge(src1, src2)` — duplicate URLs are dropped.

## Available source plugins

| Plugin | Config name | Description |
|--------|-------------|-------------|
| [`rss`](rss/README.md) | `rss` | Fetch RSS/Atom feed entries; use `url=` for a fixed feed or `url_template=` as a `discover.search` backend |
| [`html`](html/README.md) | `html` | Scrape link entries from an HTML page |
| [`filesystem`](filesystem/README.md) | `filesystem` | Walk a directory tree and emit file entries |
| [`jackett`](jackett/README.md) | `jackett` | Query Jackett indexers via Torznab; passive feed source or `discover.search` backend |
| [`trakt_list`](trakt_list/README.md) | `trakt_list` | Fetch movies or shows from a Trakt.tv list |
| [`tvdb_favorites`](tvdb_favorites/README.md) | `tvdb_favorites` | Fetch shows from a TheTVDB user's favorites list |
| [`bluray_releases`](bluray_releases/README.md) | `bluray_releases` | Scrape the Blu-ray.com release calendar; also a `discover.search` backend and a `series.list` / `movies.list` title source |
| [`series_tracker`](series_tracker/README.md) | `series_tracker` | Emit one entry per show tracked by the series tracker; also a `series.list` title source |
| [`torrent_session`](torrent_session/README.md) | `torrent_session` | Emit one entry per torrent in a Transmission/qBittorrent/Deluge session (janitor pipelines) |
| [`tvdb_calendar`](tvdb_calendar/README.md) | `tvdb_calendar` | Upcoming episodes (within a window) for shows in the series tracker, via TheTVDB air dates |
| [`trakt_calendar`](trakt_calendar/README.md) | `trakt_calendar` | Upcoming episodes from the authenticated Trakt user's my-shows calendar |
| [`webhook`](webhook/README.md) | `webhook` | Emit entries pushed to `POST /api/ingest/{queue}` (autobrr, IRC bridges, custom scripts) |
| [`run_report`](run_report/README.md) | `run_report` | One entry per traced pipeline run with counts and top rejection reasons (weekly reports) |
