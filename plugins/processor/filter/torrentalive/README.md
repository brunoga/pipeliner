# torrentalive

Rejects torrents with fewer than a minimum number of seeds. Sources seed counts in this order:

1. **Fast path** — uses the `torrent_seeds` field if already set (populated by `rss`, `jackett`, or `jackett`).
2. **Live scrape** — if `torrent_seeds` is absent and scraping is enabled, resolves the info hash and performs a live tracker scrape. The result is written back to `torrent_seeds`.

Entries where no seed count can be determined are left undecided (passed through unchanged).

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `min_seeds` | no | `1` | Minimum seed count required to accept the entry |
| `scrape` | no | `true` | Enable live tracker scraping when `torrent_seeds` is absent |
| `scrape_timeout` | no | `15s` | Per-scrape deadline |

## Example

```python
src   = input("rss", url="https://example.com/rss")
alive = process("torrentalive", upstream=src, min_seeds=3)
acc   = process("accept_all",   upstream=alive)
output("transmission", upstream=acc, host="localhost")
pipeline("seeded-only", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| MayProduce | `torrent_seeds` (updated when a live scrape runs) |
| Requires | — |
