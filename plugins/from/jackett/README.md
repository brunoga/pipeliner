# jackett

A from plugin that queries a [Jackett](https://github.com/Jackett/Jackett) indexer proxy via the Torznab API. Used as a search backend for the [`discover`](../../input/discover/README.md) input plugin via its `via` config key.

Unlike `rss_search` pointed at Jackett's RSS endpoint, this plugin speaks Torznab natively: seeder/leecher counts, info hashes, and file sizes come back in the search response, so no separate `metainfo_torrent` or `metainfo_magnet` fetch is needed.

**This plugin is a PhaseFrom sub-plugin.** It cannot be used directly as a task-level input. Use it inside `discover.via`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Jackett base URL, e.g. `http://localhost:9117` |
| `api_key` | string | yes | — | Jackett API key (found in the Jackett web UI) |
| `indexers` | list | no | `["all"]` | Indexer IDs to query. All are passed to Jackett in a single API call (comma-separated); Jackett aggregates results server-side. `"all"` queries every configured indexer. |
| `categories` | list | no | (none) | Torznab category codes to filter results. |
| `limit` | int | no | (none) | Maximum number of results to return across all indexers. |
| `timeout` | string | no | `60s` | HTTP request timeout, e.g. `60s`, `2m`. Increase when querying many indexers. |

## Example

```python
task("tv-shows", [
    plugin("discover", **{
        "from": [
            {"name": "tvdb_favorites", "api_key": "YOUR_TVDB_KEY", "user_pin": "YOUR_TVDB_PIN"},
        ],
        "via": [
            {"name": "jackett", "url": "http://localhost:9117",
             "api_key": "YOUR_JACKETT_KEY", "indexers": ["all"],
             "categories": ["5000", "5030"]},
        ],
        "interval": "12h",
    }),
    plugin("series", **{
        "from": [
            {"name": "tvdb_favorites", "api_key": "YOUR_TVDB_KEY", "user_pin": "YOUR_TVDB_PIN"},
        ],
        "quality": "720p+",
    }),
    plugin("deluge", host="localhost"),
])
```

## Entry fields set

| Field | Type | Description |
|-------|------|-------------|
| `torrent_seeders` | int | Seeder count from the indexer |
| `torrent_leechers` | int | Leecher count from the indexer |
| `torrent_size` | int | Total size in bytes |
| `torrent_info_hash` | string | SHA-1 info hash (lowercase hex), if provided |
| `torrent_link_type` | string | `"torrent"` if the entry URL serves a `.torrent` file; `"magnet"` if the Torznab `magneturl` attribute was present and `e.URL` was set to the magnet URI |
| `jackett_category` | string | Torznab category code of the result |
| `jackett_indexer` | string | Indexer ID that returned the result |

## Common Torznab categories

| Code | Category |
|------|----------|
| 2000 | Movies |
| 2010 | Movies / HD |
| 2020 | Movies / SD |
| 5000 | TV |
| 5030 | TV / HD |
| 5040 | TV / SD |
| 5045 | TV / HD |

## DAG role

`jackett` keeps `PhaseFrom` so it continues to work inside `discover.via`. Its `Role` is `source`, which means it can also be used as a standalone `input()` node in DAG pipelines:

```python
# DAG: jackett as a standalone source (no query — returns recent results)
src     = input("jackett",
    url="http://localhost:9117",
    api_key=env("JACKETT_KEY"),
    categories=["5040", "5045"],
)
quality = process("metainfo_quality", from_=src)
filtered = process("quality", from_=quality, min="720p")
output("transmission", from_=filtered, host="localhost")
pipeline("jackett-tv", schedule="1h")
```

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `torrent_seeds`, `torrent_leechers`, `torrent_info_hash`, `torrent_link_type`, `torrent_file_size` |
| Requires | — |

## Notes

- All configured indexers are queried in a single Jackett API call by passing them as a comma-separated list; Jackett aggregates results server-side.
- Category filtering is applied server-side by Jackett.
- `torrent_info_hash` being set makes `metainfo_magnet` and `metainfo_torrent` redundant for hash-based operations.
