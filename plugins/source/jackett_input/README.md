# jackett_input

See also [`jackett`](../jackett/README.md) — the Torznab search backend (also usable as a source node).

---

## `jackett_input` — direct input (recent results)

Input plugin that returns recent results from Jackett indexers without requiring a search query. Behaves like an RSS feed: each run fetches whatever the indexer considers recent for the configured categories.

### Config

Accepts all the same keys as `jackett`, plus:

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Jackett base URL |
| `api_key` | string | yes | — | Jackett API key |
| `indexers` | list | no | `["all"]` | Indexer IDs to query. All passed in a single call; Jackett aggregates results server-side. |
| `categories` | list | no | (none) | Torznab category codes to filter |
| `limit` | int | no | (none) | Maximum number of results to return across all indexers. |
| `timeout` | string | no | `60s` | HTTP request timeout, e.g. `60s`, `2m`. Increase when querying many indexers. |
| `query` | string | no | `""` | Optional search query; empty returns all recent results |

### Example

```python
task("tv-discover", [
    plugin("jackett_input",
        url="http://localhost:9117",
        api_key="YOUR_JACKETT_KEY",
        indexers=["all"],
        categories=["5040", "5045"],
    ),
    plugin("premiere", quality="720p+ webrip+"),
    plugin("email",
        smtp_host="smtp.example.com",
        **{"from": "me@example.com"},
        to="me@example.com",
    ),
])
```

## DAG role (`jackett_input`)

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `published_date`, `torrent_seeds`, `torrent_leechers`, `torrent_info_hash`, `torrent_link_type`, `torrent_file_size` |
| Requires | — |

DAG example:
```python
src = input("jackett_input",
    url="http://localhost:9117",
    api_key=env("JACKETT_KEY"),
    categories=["5040", "5045"],
)
output("print", from_=src)
pipeline("jackett-feed", schedule="1h")
```

---

## Entry fields set (both plugins)

| Field | Type | Description |
|-------|------|-------------|
| `torrent_seeds` | int | Seeder count from the indexer |
| `torrent_leechers` | int | Leecher count from the indexer |
| `torrent_file_size` | int64 | Total size in bytes |
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

## Notes

- All configured indexers are queried in a single Jackett API call by passing them as a comma-separated list; Jackett aggregates results server-side.
- Category filtering is applied server-side by Jackett.
- The error response body is included in the log when Jackett returns a non-200 status, making misconfiguration easier to diagnose.
- `torrent_info_hash` being set makes `metainfo_magnet` and `metainfo_torrent` redundant for hash-based operations.
