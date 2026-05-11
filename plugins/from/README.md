# Source plugins (from/)

From-plugins are source plugins that can be used in two ways:

1. **Standalone DAG source** — as an `input()` node in a pipeline
2. **Config-driven title source** — inside `series.from`, `movies.from`, and
   `discover.from` config keys to supply dynamic title lists
3. **Search backend** — inside `discover.via` (those implementing `SearchPlugin`)

## Available from plugins

| Plugin | Config name | Role | Description |
|--------|-------------|------|-------------|
| [tvdb](tvdb/README.md) | `tvdb_favorites` | source | Fetch TheTVDB favorites as show-name entries |
| [trakt](trakt/README.md) | `trakt_list` | source | Fetch movies or shows from a Trakt.tv list |
| [jackett](jackett/README.md) | `jackett` | source | Query Jackett indexers via Torznab (also a search backend for discover.via) |
| [rss](rss/README.md) | `rss_search` | source | Search a parameterized RSS URL (also a search backend for discover.via) |

## Usage as a standalone DAG source

```python
shows = input("tvdb_favorites", api_key=env("TVDB_KEY"), user_pin=env("TVDB_PIN"))
series_flt = process("series", from_=shows, tracking="strict")
output("transmission", from_=series_flt, host="localhost")
pipeline("tv-tvdb", schedule="1h")
```

## Usage inside parent plugin config

```python
# In a DAG config, prefer connecting source nodes directly (above).
# The 'from' config key is an alternative for simpler cases:
series_proc = process("series", from_=rss_src, **{"from": [
    {"name": "trakt_list", "client_id": env("TRAKT_ID"),
     "client_secret": env("TRAKT_SECRET"), "type": "shows"},
]})
```

## Search backends (discover.via)

`jackett` and `rss_search` also implement `SearchPlugin`, allowing them to be
used as search backends inside `discover.via`:

```python
results = process("discover", from_=watchlist,
    via=[{"name": "jackett", "url": "http://localhost:9117",
          "api_key": env("JACKETT_KEY"), "categories": ["5040"]}],
    interval="6h")
```
