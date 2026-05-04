# jackett

Search plugin that queries a [Jackett](https://github.com/Jackett/Jackett) indexer proxy via its Torznab API. Unlike `search_rss` pointed at Jackett's RSS endpoint, this plugin speaks Torznab natively: seeder/leecher counts, info hashes, and file sizes come back in the search response, so no separate `metainfo_torrent` or `metainfo_magnet` fetch is needed.

Used as a backend for the [`discover`](../../discover/README.md) input plugin.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Jackett base URL, e.g. `http://localhost:9117` |
| `api_key` | string | yes | — | Jackett API key (found in the Jackett web UI) |
| `indexers` | list | no | `[all]` | Indexer IDs to query. `all` searches every configured indexer in one request. |
| `categories` | list | no | (none) | Torznab category codes to filter results. See table below. |

### Common Torznab categories

| Code | Category |
|------|----------|
| 2000 | Movies |
| 2010 | Movies / HD |
| 2020 | Movies / SD |
| 5000 | TV |
| 5030 | TV / HD |
| 5040 | TV / SD |

## Entry fields set

| Field | Type | Description |
|-------|------|-------------|
| `torrent_seeders` | int | Seeder count from the indexer |
| `torrent_leechers` | int | Leecher count from the indexer |
| `torrent_size` | int | Total size in bytes |
| `torrent_info_hash` | string | SHA-1 info hash (lowercase hex), if provided by the indexer |
| `jackett_category` | string | Torznab category code of the result |
| `jackett_indexer` | string | Indexer ID that returned this result |

`torrent_info_hash` is set directly from the Torznab response when the indexer provides it, making `metainfo_magnet` or `metainfo_torrent` redundant for hash-based operations.

## Example

```yaml
variables:
  jackett_url: "http://localhost:9117"
  jackett_key: "{$ JACKETT_API_KEY $}"

tasks:
  tv-shows:
    discover:
      from: series
      via:
        - jackett:
            url: "{$ jackett_url $}"
            api_key: "{$ jackett_key $}"
            indexers: [all]
            categories: [5000, 5030]
      interval: 12h
    seen:
    series:
      shows:
        - "Breaking Bad"
        - "Better Call Saul"
    quality:
      min: 720p
    transmission:
      host: localhost
```

## Notes

- If multiple indexers are configured, each is queried separately and results are deduplicated by URL. A failing indexer is logged and skipped; results from the remaining indexers are still returned.
- Category filtering is applied server-side by Jackett, reducing the result set before it reaches pipeliner.
- The `torrent_seeders` field can be used with `condition` to gate on minimum seeders: `accept: 'torrent_seeders >= 5'`.
