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
results = process("discover", from_=rss_src, **{"from": [
    {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
     "access_token": "YOUR_ACCESS_TOKEN", "type": "movies", "list": "watchlist"},
], "via": [...]})
```

### `via` entries

Each entry references a registered [search plugin](../search/). Either a plugin name string or an object:

```python
results = process("discover", from_=rss_src, **{"via": [
    "rss_search",    # name only, uses defaults
    {"name": "rss_search",
     "url_template": "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"},
]})
```

## Example — static title list

```python
watchlist = input("trakt_list", client_id=env("TRAKT_ID"),
                  client_secret=env("TRAKT_SECRET"),
                  type="movies", list="watchlist")
results   = process("discover", from_=watchlist,
    via=[{"name": "jackett", "url": "http://localhost:9117",
          "api_key": env("JACKETT_KEY"), "categories": ["2000"]}],
    interval="12h")
seen      = process("seen",            from_=results)
q         = process("metainfo_quality", from_=seen)
flt       = process("quality",          from_=q, min="1080p")
output("qbittorrent", from_=flt, host="localhost")
pipeline("movie-discover", schedule="2h")
```

## Example — dynamic title list from Trakt watchlist

## DAG role

`discover` acts as a **processor** in DAG pipelines. Upstream source nodes supply
the title list; `discover` searches for each title via the `via` backends and
returns the found entries (not the upstream entries themselves).

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `torrent_seeds`, `torrent_info_hash`, `torrent_link_type` (and whatever the `via` search plugins return) |
| Requires | — |

### DAG example

```python
# Upstream source: Trakt watchlist provides titles.
watchlist = input("trakt_list",
    client_id=env("TRAKT_CLIENT_ID"),
    client_secret=env("TRAKT_SECRET"),
    type="shows", list="watchlist")

# discover receives those entries, searches Jackett for each title,
# and returns torrent results (not the Trakt entries).
results = process("discover", from_=watchlist,
    via=[{"name": "jackett",
          "url":     "http://localhost:9117",
          "api_key": env("JACKETT_KEY"),
          "categories": ["5040", "5045"]}],
    interval="6h")

seen  = process("seen",   from_=results)
seen2 = process("series", from_=seen, static=["Breaking Bad"])
output("transmission", from_=seen2, host="localhost")
pipeline("tv-discover", schedule="1h")
```

## Example — combined static and dynamic

