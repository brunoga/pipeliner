# metainfo_torrent

Reads metadata from a `.torrent` file. If the entry came from the `filesystem` plugin, reads the file at the `location` field. Otherwise, downloads the URL if it ends in `.torrent`. Takes no config.

## Fields set on entry

| Field | Type | Description |
|-------|------|-------------|
| `torrent_name` | string | Torrent name (top-level file or directory) |
| `torrent_info_hash` | string | SHA-1 info hash (hex) |
| `torrent_size` | int | Total content size in bytes |
| `torrent_file_count` | int | Number of files in the torrent |
| `torrent_announce` | string | Primary tracker URL |
| `torrent_comment` | string | Torrent comment (if set) |
| `torrent_created_by` | string | Client that created the torrent (if set) |
| `torrent_creation_date` | int | Unix timestamp of creation (if set) |
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
