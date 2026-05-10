# discover

Actively searches multiple sources for entries matching a list of titles. Unlike RSS-based inputs that passively receive all items, `discover` iterates a title list, dispatches a search query per title to each configured search plugin, and returns the merged, deduplicated results.

A per-title cooldown (`interval`) prevents redundant searches on successive runs.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `titles` | list | conditional | — | Static list of title strings to search for |
| `from` | list | conditional | — | Input plugin configs whose entry titles supplement the title list |
| `via` | list | yes | — | Search plugins to query |
| `interval` | string | no | `24h` | Minimum time between searches per title |

At least one of `titles` or `from` must produce titles. The combined title list is deduplicated case-insensitively before searching. Search timestamps are stored in `pipeliner.db` in the same directory as the config file.

### `from` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config. The entry titles returned by those plugins are added to the search queue:

```python
plugin("discover", **{"from": [
    {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
     "access_token": "YOUR_ACCESS_TOKEN", "type": "movies", "list": "watchlist"},
], "via": [...]})
```

### `via` entries

Each entry references a registered [search plugin](../search/). Either a plugin name string or an object:

```python
plugin("discover", **{"via": [
    "rss_search",    # name only, uses defaults
    {"name": "rss_search",
     "url_template": "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"},
]})
```

## Example — static title list

```python
task("movies-discover", [
    plugin("discover", **{
        "titles": ["Dune Part Two", "Oppenheimer"],
        "via": [
            {"name": "rss_search",
             "url_template": "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"},
        ],
        "interval": "12h",
    }),
    plugin("metainfo_quality"),
    plugin("quality", min="1080p"),
    plugin("seen"),
    plugin("qbittorrent", host="localhost"),
])
```

## Example — dynamic title list from Trakt watchlist

```python
task("discover-watchlist", [
    plugin("discover", **{
        "from": [
            {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
             "access_token": "YOUR_ACCESS_TOKEN", "type": "movies", "list": "watchlist"},
        ],
        "via": [
            {"name": "rss_search",
             "url_template": "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"},
        ],
        "interval": "6h",
    }),
    plugin("metainfo_quality"),
    plugin("quality", min="1080p"),
    plugin("seen"),
    plugin("qbittorrent", host="localhost"),
])
```

## Example — combined static and dynamic

```python
task("discover-combined", [
    plugin("discover", **{
        "titles": ["Severance"],       # always searched regardless of watchlist
        "from": [
            {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
             "access_token": "YOUR_ACCESS_TOKEN", "type": "shows", "list": "watchlist"},
        ],
        "via": [
            {"name": "rss_search",
             "url_template": "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"},
        ],
        "interval": "12h",
    }),
    plugin("series", tracking="strict"),
    plugin("qbittorrent", host="localhost"),
])
```
