# jackett / jackett_input

This package provides two plugins for interacting with a [Jackett](https://github.com/Jackett/Jackett) indexer proxy via the Torznab API.

---

## `jackett` — search backend for `discover`

Search plugin that queries Jackett indexers on demand. Unlike `search_rss` pointed at Jackett's RSS endpoint, this plugin speaks Torznab natively: seeder/leecher counts, info hashes, and file sizes come back in the search response, so no separate `metainfo_torrent` or `metainfo_magnet` fetch is needed.

Used as a backend for the [`discover`](../../discover/README.md) input plugin.

### Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Jackett base URL, e.g. `http://localhost:9117` |
| `api_key` | string | yes | — | Jackett API key (found in the Jackett web UI) |
| `indexers` | list | no | `["all"]` | Indexer IDs to query. `"all"` searches every configured indexer. |
| `categories` | list | no | (none) | Torznab category codes to filter results. |

### Example

```yaml
tasks:
  tv-shows:
    discover:
      from:
        - name: input_tvdb
          api_key: YOUR_TVDB_KEY
          user_pin: YOUR_TVDB_PIN
      via:
        - name: jackett
          url: "http://localhost:9117"
          api_key: YOUR_JACKETT_KEY
          indexers: ["all"]
          categories: ["5000", "5030"]
      interval: 12h
    series:
      from:
        - name: input_tvdb
          api_key: YOUR_TVDB_KEY
          user_pin: YOUR_TVDB_PIN
      quality: 720p+
    deluge:
      host: localhost
```

---

## `jackett_input` — direct input (recent results)

Input plugin that returns recent results from Jackett indexers without requiring a search query. Behaves like an RSS feed: each run fetches whatever the indexer considers recent for the configured categories.

### Config

Accepts all the same keys as `jackett`, plus:

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Jackett base URL |
| `api_key` | string | yes | — | Jackett API key |
| `indexers` | list | no | `["all"]` | Indexer IDs to query |
| `categories` | list | no | (none) | Torznab category codes to filter |
| `query` | string | no | `""` | Optional search query; empty returns all recent results |

### Example

```yaml
tasks:
  tv-discover:
    jackett_input:
      url: "http://localhost:9117"
      api_key: YOUR_JACKETT_KEY
      indexers: ["all"]
      categories: ["5040", "5045"]
    premiere:
      quality: 720p+ webrip+
    email:
      smtp_host: smtp.example.com
      from: me@example.com
      to: me@example.com
```

---

## Entry fields set (both plugins)

| Field | Type | Description |
|-------|------|-------------|
| `torrent_seeders` | int | Seeder count from the indexer |
| `torrent_leechers` | int | Leecher count from the indexer |
| `torrent_size` | int | Total size in bytes |
| `torrent_info_hash` | string | SHA-1 info hash (lowercase hex), if provided |
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

- If multiple indexers are configured, each is queried separately and results are deduplicated by URL. A failing indexer is logged and skipped.
- Category filtering is applied server-side by Jackett.
- The error response body is included in the log when Jackett returns a non-200 status, making misconfiguration easier to diagnose.
- `torrent_info_hash` being set makes `metainfo_magnet` and `metainfo_torrent` redundant for hash-based operations.
