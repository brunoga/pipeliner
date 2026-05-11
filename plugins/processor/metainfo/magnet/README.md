# metainfo_magnet

Annotates entries whose URL is a magnet link with torrent metadata. URI-derived fields (info hash, tracker list, display name) are set immediately from the magnet URI itself. Full metadata (name, size, file list) is resolved via DHT when peers can be found within the configured timeout.

All DHT lookups for a batch of entries are dispatched in parallel, sharing a single timeout window, so a batch of 20 magnet links costs the same wall-clock time as a single one.

An entry is treated as a magnet when `torrent_link_type = "magnet"` (set by `jackett` / `jackett_input`) or when the URL starts with `magnet:`. Entries with `torrent_link_type = "torrent"` or a non-magnet URL are silently skipped.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `resolve_timeout` | string | no | `30s` | Maximum wall-clock time to wait for DHT metadata across all entries in a batch |

## Fields set on each entry

### Always (from the magnet URI)

| Field | Type | Description |
|-------|------|-------------|
| `torrent_info_hash` | string | Hex SHA-1 info hash (40 chars) |
| `torrent_announce` | string | First tracker announce URL |
| `torrent_announce_list` | []string | All tracker announce URLs |
| `title` | string | Human-readable name from the `dn=` parameter |

### After DHT resolution (when peers respond within the timeout)

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Name from the info dict (overrides `dn=` value if resolved) |
| `torrent_file_size` | int64 | Total size in bytes |
| `torrent_file_count` | int | Number of files |
| `torrent_files` | []string | File paths relative to the torrent root |

DHT resolution fields are absent when the timeout expires before peers respond. The entry is still passed to output plugins with only the URI-derived fields.

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `torrent_info_hash`, `torrent_announce`, `torrent_announce_list`, `torrent_file_size`, `torrent_file_count`, `torrent_files` |
| Requires | — |

## Example

```python
src     = input("rss", url="https://example.com/rss/magnets")
seen    = process("seen", from_=src)
magnet  = process("metainfo_magnet", from_=seen, resolve_timeout="45s")
quality = process("metainfo_quality", from_=magnet)
flt     = process("quality", from_=quality, min="720p")
output("qbittorrent", from_=flt, host="localhost")
pipeline("magnets", schedule="1h")
```

## Notes

- DHT resolution requires outbound UDP access to the BitTorrent DHT network. Entries with no reachable peers time out and retain only URI-derived fields.
- The plugin creates a single shared torrent client at plugin construction time. The client uses an ephemeral listen port and never uploads or seeds data.
- `resolve_timeout` applies to the entire batch, not per entry. Set it higher when processing large batches.
