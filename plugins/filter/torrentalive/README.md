# torrent_alive

Rejects torrents with fewer than a minimum number of seeds.

The plugin sources seed counts in this order:

1. **Fast path** — uses the `torrent_seeds` field if already set (populated by the `rss` input plugin from torrent namespace extensions in feeds from nyaa, Jackett, ezrss, etc.).
2. **Live scrape** — if `torrent_seeds` is absent and scraping is enabled, the info hash and announce list are resolved from the entry URL (magnet URIs only; no network call required for those), and a live tracker scrape is performed. The result is written back to `torrent_seeds`.

Entries where no seed count can be determined are left undecided (no-op).

## Config

```python
plugin("torrent_alive",
    min_seeds=5,         # minimum seeds required (default: 1)
    scrape=True,         # enable live tracker scraping when torrent_seeds absent (default: true)
    scrape_timeout="15s",  # per-scrape deadline (default: 15s)
)
```

## Fields read and written

| Field | Direction | Description |
|-------|-----------|-------------|
| `torrent_seeds` | read | Feed-provided seed count (fast path) |
| `torrent_info_hash` | read/write | Info hash used for scraping; auto-populated from magnet URIs |
| `torrent_announce` | read/write | Primary tracker URL; auto-populated from magnet URIs |
| `torrent_announce_list` | read/write | All tracker announce URLs; auto-populated from magnet URIs |
| `torrent_seeds` | write | Updated with live seed count after a successful scrape |

## Example

```python
task("anime", [
    plugin("rss", url="https://nyaa.si/?page=rss&cats=1_2&filter=2"),
    plugin("torrent_alive", min_seeds=3),
    plugin("series", static=["My Hero Academia"]),
])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `torrent_seeds`, `torrent_leechers` |
| Requires | — |
