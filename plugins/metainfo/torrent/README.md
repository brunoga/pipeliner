# metainfo_torrent

Reads metadata from a `.torrent` file. If the entry came from the `filesystem` plugin, reads the file at the `file_location` field. Otherwise, downloads the URL if it ends in `.torrent`.

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

## Example

```yaml
tasks:
  watch-folder:
    filesystem:
      path: /downloads/watch
      mask: "*.torrent"
    metainfo_torrent:
    condition:
      reject: '{{.torrent_private}}'   # skip private torrents
    transmission:
      host: localhost
```
