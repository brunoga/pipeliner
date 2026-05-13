# metainfo_torrent

Reads metadata from a `.torrent` file and annotates entries with its contents. It recognises an entry as a torrent download using three signals, checked in order:

1. `file_location` field ends in `.torrent` (set by the `filesystem` plugin) — reads the local file
2. `torrent_link_type = "torrent"` (set by `jackett` / `jackett_input`) — fetches the URL
3. URL ends in `.torrent` or `rss_enclosure_type = "application/x-bittorrent"` — fetches the URL

Entries with `torrent_link_type = "magnet"` are always skipped (handled by `metainfo_magnet`).

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `fetch_timeout` | string | no | `30s` | HTTP timeout for downloading `.torrent` files |

## Fields set on entry

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Torrent name (top-level file or directory) |
| `description` | string | Torrent comment field (if set) |
| `torrent_info_hash` | string | SHA-1 info hash (hex) |
| `torrent_file_size` | int64 | Total content size in bytes |
| `torrent_file_count` | int | Number of files in the torrent |
| `torrent_files` | []string | Relative file paths within the torrent |
| `torrent_announce` | string | Primary tracker URL |
| `torrent_announce_list` | []string | All tracker announce URLs |
| `torrent_created_by` | string | Client that created the torrent (if set) |
| `torrent_creation_date` | time.Time | Creation timestamp (if set) |
| `torrent_private` | bool | Whether the torrent is private |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `title`, `torrent_info_hash`, `torrent_file_size`, `torrent_file_count`, `torrent_files`, `torrent_announce`, `torrent_announce_list`, `torrent_created_by`, `torrent_creation_date`, `torrent_private` |
| Requires | — |

## Example

```python
src  = input("filesystem", path="/downloads/watch", mask="*.torrent")
meta = process("metainfo_torrent", upstream=src)
output("transmission", upstream=meta, host="localhost")
pipeline("watch-folder", schedule="5m")
```
