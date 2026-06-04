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
