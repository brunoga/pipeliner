# Source plugins (`plugins/source/`)

Source plugins produce entries. Use them with `input("plugin-name", …)` in a pipeline.
Combine multiple sources with `merge(src1, src2)` — duplicate URLs are dropped.

## Available source plugins

| Plugin | Config name | Description |
|--------|-------------|-------------|
| [`rss`](rss/README.md) | `rss` | Fetch entries from an RSS/Atom feed |
| [`html`](html/README.md) | `html` | Scrape link entries from an HTML page |
| [`filesystem`](filesystem/README.md) | `filesystem` | Walk a directory tree and emit file entries |
| [`jackett`](jackett/README.md) | `jackett` | Fetch recent results from Jackett indexers as a passive feed |
| [`trakt_list`](trakt_list/README.md) | `trakt_list` | Fetch movies or shows from a Trakt.tv list |
| [`tvdb_favorites`](tvdb_favorites/README.md) | `tvdb_favorites` | Fetch shows from a TheTVDB user's favorites list |
| [`jackett_search`](jackett_search/README.md) | `jackett_search` | Search Jackett via Torznab (also a search backend for `discover.via`) |
| [`rss_search`](rss_search/README.md) | `rss_search` | Search a parameterized RSS URL (also a backend for `discover.via`) |
