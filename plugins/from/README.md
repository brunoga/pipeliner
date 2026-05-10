# From plugins

From plugins are sub-plugins used as data sources inside `series.from`, `movies.from`, and `discover.from` (and as search backends inside `discover.via`). They are registered with `PhaseFrom` and **cannot** be used as top-level task plugins — the task engine will report an error if a PhaseFrom plugin is referenced directly in a task.

From plugins implement either `InputPlugin` (have a `Run` method, used in `from:` lists) or `SearchPlugin` (have a `Search` method, used in `via:` lists).

## Available from plugins

| Plugin | Config name | Used in | Description |
|--------|-------------|---------|-------------|
| [tvdb](tvdb/README.md) | `tvdb_favorites` | `series.from`, `discover.from` | Fetch TheTVDB favorites as show-name entries |
| [trakt](trakt/README.md) | `trakt_list` | `series.from`, `movies.from`, `discover.from` | Fetch movies or shows from a Trakt.tv list |
| [jackett](jackett/README.md) | `jackett` | `discover.via` | Query Jackett indexers via the Torznab API |
| [rss](rss/README.md) | `rss_search` | `discover.via` | Search a parameterized RSS URL |

## Usage pattern

From plugins are specified by name (string form) or as a config dict inside the parent plugin's `from` or `via` list:

```python
plugin("series", **{"from": [
    "tvdb_favorites",                  # name only, uses defaults
    {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
     "access_token": "YOUR_TOKEN", "type": "shows", "list": "watchlist"},
]})

plugin("discover", **{
    "from": [
        {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
         "type": "movies", "list": "watchlist"},
    ],
    "via": [
        {"name": "rss_search",
         "url_template": "https://example.com/search?q={{.QueryEscaped}}"},
        {"name": "jackett", "url": "http://localhost:9117",
         "api_key": "YOUR_JACKETT_KEY"},
    ],
})
```
