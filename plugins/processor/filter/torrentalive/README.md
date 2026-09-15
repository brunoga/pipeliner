# torrent_alive

Rejects torrents with fewer than a minimum number of seeds. Sources seed counts in this order:

1. **Fast path** — uses the `torrent_seeds` field if already set (populated by `rss` or `jackett`). Skipped when `verify=True`.
2. **Live scrape** — if `torrent_seeds` is absent (or `verify=True`) and scraping is enabled, resolves the info hash and performs a live tracker scrape. The result is written back to `torrent_seeds`.

Indexer-reported counts can be stale or phantom, especially for rare releases: two claimed seeders may be long gone, letting the grab pass and then sit at 0% forever. `verify=True` forces a live scrape even when the feed provides a count; the scrape result wins, and the feed count is used only as a fallback when scraping is impossible (no resolvable hash, or the tracker did not answer). Pair it with `metainfo_torrent` upstream so `.torrent` URL entries have a hash to scrape, and reserve it for low-volume pipelines — each entry costs a scrape round-trip.

Scraping is **batched**: entries are grouped by tracker and each request carries up to ~70 info hashes (both the UDP protocol and the HTTP scrape convention support this natively), with distinct trackers queried in parallel. A discover-scale run of ~2000 entries against one tracker costs ~30 requests instead of 2000 sequential ones. `scrape_timeout` bounds each tracker request, not each entry.

Entries where no seed count can be determined are left undecided (passed through unchanged).

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `min_seeds` | no | `1` | Minimum seed count required to accept the entry |
| `scrape` | no | `true` | Enable live tracker scraping when `torrent_seeds` is absent |
| `verify` | no | `false` | Always scrape to verify feed-provided seed counts; falls back to the feed count when scraping is impossible |
| `scrape_timeout` | no | `15s` | Deadline per tracker request (a request covers up to ~70 entries) |

## Example

```python
src   = input("rss", url="https://example.com/rss")
alive = process("torrent_alive", upstream=src, min_seeds=3)
acc   = process("accept_all",   upstream=alive)
output("transmission", upstream=acc, host="localhost")
pipeline("seeded-only", schedule="1h")
```

Rare-release pipeline where the indexer's count is not trustworthy:

```python
found = process("discover", upstream=wanted, search=[...])
meta  = process("metainfo_torrent", upstream=found)
alive = process("torrent_alive", upstream=meta, min_seeds=2, verify=True)
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| MayProduce | `torrent_seeds` (updated when a live scrape runs) |
| Requires | — |
