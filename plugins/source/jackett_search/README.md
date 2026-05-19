# jackett_search

Queries a [Jackett](https://github.com/Jackett/Jackett) indexer proxy via the Torznab API with an explicit search query. Used as a search backend for the [`discover`](../../processor/discover/README.md) plugin, and can also be used as a standalone `input()` source node (queries with an empty string to return recent results).

Unlike pointing `rss_search` at Jackett's RSS endpoint, this plugin speaks Torznab natively: seeder/leecher counts, info hashes, and file sizes come back in the search response — no separate `metainfo_torrent` or `metainfo_magnet` fetch is needed.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `url` | yes | — | Jackett base URL (e.g. `http://localhost:9117`) |
| `api_key` | yes | — | Jackett API key (found in the Jackett web UI) |
| `indexers` | no | `["all"]` | Indexer IDs to query; Jackett aggregates results server-side |
| `categories` | no | — | Torznab category codes to filter (e.g. `["5040","5045"]` for TV HD/SD) |
| `limit` | no | — | Maximum number of results across all indexers |
| `timeout` | no | `60s` | HTTP request timeout |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `torrent_seeds` | Seeder count |
| `torrent_leechers` | Leecher count |
| `torrent_file_size` | Total size in bytes |
| `torrent_info_hash` | SHA-1 info hash, if provided |
| `torrent_link_type` | `"torrent"` or `"magnet"` |
| `jackett_category` | Torznab category code |
| `jackett_indexer` | Source indexer name |

## Common Torznab categories

| Code | Category |
|------|----------|
| 2000 | Movies |
| 2010 | Movies / HD |
| 5000 | TV |
| 5030 | TV / HD |
| 5040 | TV / SD |
| 5045 | TV / UHD |

## Example — as a discover search backend

```python
watchlist = input("trakt_list", client_id=env("TRAKT_ID"),
                  client_secret=env("TRAKT_SECRET"),
                  type="shows", list="watchlist")
results   = process("discover", upstream=watchlist,
    search=[{"name": "jackett_search",
             "url":     "http://localhost:9117",
             "api_key": env("JACKETT_KEY"),
             "categories": ["5040", "5045"]}],
    interval="6h")
seen   = process("seen",   upstream=results)
series = process("series", upstream=seen,
    list=[{"name": "trakt_list", "client_id": env("TRAKT_ID"),
           "client_secret": env("TRAKT_SECRET"), "type": "shows"}])
output("transmission", upstream=series, host="localhost")
pipeline("tv-discover", schedule="2h")
```

## Example — as a standalone source (recent results)

```python
src     = input("jackett_search", url="http://localhost:9117",
                api_key=env("JACKETT_KEY"), categories=["5040", "5045"])
quality = process("metainfo_quality", upstream=src)
flt     = process("quality", upstream=quality, min="720p")
output("transmission", upstream=flt, host="localhost")
pipeline("jackett-tv", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `torrent_seeds`, `torrent_leechers`, `torrent_file_size`, `torrent_info_hash`, `torrent_link_type`, `jackett_category`, `jackett_indexer` |
| Requires | — |

## Notes

- All configured indexers are queried in a single Jackett API call; Jackett aggregates results server-side.
- Category filtering is applied server-side by Jackett.
- `torrent_info_hash` being set makes separate `metainfo_torrent` or `metainfo_magnet` fetches unnecessary for hash-based operations.
