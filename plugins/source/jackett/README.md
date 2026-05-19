# jackett

Returns recent results from Jackett indexers without requiring a search query. Behaves like an RSS feed: each run fetches whatever the configured indexers consider recent for the configured categories.

See also [`jackett_search`](../jackett_search/README.md) — the Torznab search backend for active searching via the [`discover`](../../processor/discover/README.md) plugin.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `url` | yes | — | Jackett base URL (e.g. `http://localhost:9117`) |
| `api_key` | yes | — | Jackett API key (found in the Jackett web UI) |
| `indexers` | no | `["all"]` | Indexer IDs to query; Jackett aggregates results server-side |
| `categories` | no | — | Torznab category codes to filter |
| `limit` | no | — | Maximum number of results across all indexers |
| `timeout` | no | `60s` | HTTP request timeout |
| `query` | no | `""` | Optional search query; empty returns all recent results |

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

## Example

```python
src     = input("jackett", url="http://localhost:9117",
                api_key=env("JACKETT_KEY"), categories=["5040", "5045"])
seen    = process("seen",   upstream=src)
quality = process("metainfo_quality", upstream=seen)
prem    = process("premiere", upstream=quality, quality="720p+ webrip+")
best    = process("dedup",  upstream=prem)
output("transmission", upstream=best, host="localhost")
pipeline("new-shows", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `torrent_seeds`, `torrent_leechers`, `torrent_file_size`, `torrent_info_hash`, `torrent_link_type`, `jackett_category`, `jackett_indexer` |
| MayProduce | `published_date` |
| Requires | — |

## Notes

- All configured indexers are queried in a single Jackett API call; Jackett aggregates results server-side.
- Category filtering is applied server-side by Jackett.
- `torrent_info_hash` being set makes separate `metainfo_torrent` or `metainfo_magnet` fetches unnecessary for hash-based operations.
