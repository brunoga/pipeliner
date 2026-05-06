# Input plugins

Input plugins produce entries. A task can have one or more input plugins; all of them run concurrently and their results are merged, with duplicate URLs deduplicated. An entry carries a title, a URL, and an extensible field map. All entries start in the **undecided** state.

| Plugin | Description |
|--------|-------------|
| [rss](rss/README.md) | Fetch entries from an RSS/Atom feed |
| [html](html/README.md) | Scrape link entries from an HTML page using CSS selectors |
| [filesystem](filesystem/README.md) | Walk a directory tree and emit one entry per file |
| [discover](discover/README.md) | Actively search multiple sources for a configured title list |
| [jackett_input](search/jackett/README.md) | Fetch recent results from Jackett indexers as a passive feed |

## Active vs passive

`rss`, `html`, and `filesystem` are **passive** — they return whatever the source gives them and rely on filters to narrow results down.

`discover` is **active** — it drives outbound searches for specific titles, useful when you want a particular movie or show and don't want to wait for it to appear in a feed.

`trakt_list`, `tvdb_favorites`, `jackett`, and `rss_search` are **from plugins** — they are not standalone task inputs but are used as sub-plugins inside `series.from`, `movies.from`, `discover.from`, and `discover.via`. See [`plugins/from/`](../from/README.md) for details.
