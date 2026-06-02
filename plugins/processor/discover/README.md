# discover

Actively searches multiple sources for entries matching a list of titles. Unlike RSS-based inputs that passively receive all items, `discover` iterates a title list, dispatches a search query per title to each configured search plugin, and returns the merged, deduplicated results.

A per-title cooldown (`interval`) prevents redundant searches on successive runs. Within the cooldown the previously-returned results are served from a cache so the rest of the pipeline still has entries to act on — the cooldown throttles the indexer, not the pipeline.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `titles` | list | conditional | — | Static list of title strings to search for |
| `search` | list | yes | — | Search plugins to query |
| `interval` | string | no | `24h` | Minimum time between live searches per title (cached results are still emitted between live searches) |

Titles come from `titles=` (static) and the `.Title` field of every upstream entry. At least one source must produce titles. The combined title list is deduplicated case-insensitively before searching. Per-title results and the timestamp of the last live search are stored in `pipeliner.db` in the same directory as the config file. Dry-run mode bypasses this cache entirely (read and write) so a dry-run exercises the backends end-to-end without poisoning subsequent real runs.

### `search` entries

Each entry references a registered [search plugin](../search/). Either a plugin name string or an object:

```python
results = process("discover", upstream=rss_src,
    search=[
        "rss",    # name only, uses defaults
        {"name": "rss",
         "url_template": "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"},
    ])
```

## Example — static title list

```python
watchlist = input("trakt_list", client_id=env("TRAKT_ID"),
                  client_secret=env("TRAKT_SECRET"),
                  type="movies", list="watchlist")
results   = process("discover", upstream=watchlist,
    search=[{"name": "jackett", "url": "http://localhost:9117",
             "api_key": env("JACKETT_KEY"), "categories": ["2000"]}],
    interval="12h")
seen      = process("seen",          upstream=results)
meta      = process("metainfo_file", upstream=seen)
flt       = process("movies",        upstream=meta,
                    static=["Inception"], quality="1080p+")
output("qbittorrent", upstream=flt, host="localhost")
pipeline("movie-discover", schedule="2h")
```

## DAG role

`discover` acts as a **processor** in DAG pipelines. Upstream source nodes supply
the title list; `discover` searches for each title via the `search` backends and
returns the found entries (not the upstream entries themselves).

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `torrent_seeds`, `torrent_info_hash`, `torrent_link_type` (and whatever the `search` plugins return) |
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
results = process("discover", upstream=watchlist,
    search=[{"name": "jackett",
             "url":     "http://localhost:9117",
             "api_key": env("JACKETT_KEY"),
             "categories": ["5040", "5045"]}],
    interval="6h")

seen  = process("seen",          upstream=results)
meta  = process("metainfo_file", upstream=seen)
srs   = process("series",        upstream=meta, static=["Breaking Bad"])
output("transmission", upstream=srs, host="localhost")
pipeline("tv-discover", schedule="1h")
```

## Example — combined static and dynamic

```python
# Static titles always searched; Trakt watchlist adds more dynamically.
watchlist = input("trakt_list", client_id=env("TRAKT_ID"),
                  client_secret=env("TRAKT_SECRET"),
                  type="movies", list="watchlist")
results   = process("discover", upstream=watchlist,
    titles=["Dune Part Two", "Oppenheimer"],
    search=[{"name": "jackett",
             "url":     "http://localhost:9117",
             "api_key": env("JACKETT_KEY"),
             "categories": ["2000"]}],
    interval="12h")
seen    = process("seen",   upstream=results)
movies  = process("movies", upstream=seen,
    static=["Dune Part Two", "Oppenheimer"],
    list=[{"name": "trakt_list", "client_id": env("TRAKT_ID"),
           "client_secret": env("TRAKT_SECRET"), "type": "movies", "list": "watchlist"}],
    quality="1080p+")
output("qbittorrent", upstream=movies, host="localhost")
pipeline("movie-discover", schedule="2h")
```

