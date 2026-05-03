# Input plugins

Input plugins produce entries. Each task has exactly one input plugin. An entry carries a title, a URL, and an extensible field map. All entries returned by the input start in the **undecided** state.

| Plugin | Description |
|--------|-------------|
| [rss](rss/README.md) | Fetch entries from an RSS/Atom feed |
| [html](html/README.md) | Scrape link entries from an HTML page using CSS selectors |
| [filesystem](filesystem/README.md) | Walk a directory tree and emit one entry per file |
| [discover](discover/README.md) | Actively search multiple sources for a configured title list |
| [input_trakt](trakt/README.md) | Fetch movies or shows from a Trakt.tv list |
| [input_tvdb](tvdb/README.md) | Fetch shows from a TheTVDB user's favorites list |
| [search_rss](search/rss/README.md) | RSS search backend driven by query terms from `discover` |

## Active vs passive

`rss`, `html`, and `filesystem` are **passive** — they return whatever the source gives them and rely on filters to narrow results down.

`discover` is **active** — it drives outbound searches for specific titles, useful when you want a particular movie or show and don't want to wait for it to appear in a feed.

`input_trakt` and `input_tvdb` are **list sources** — they emit title entries suitable for use as dynamic title feeds for `discover.from`, `series.from`, and `movies.from`. They can also be used as standalone inputs to inspect or print list contents.
