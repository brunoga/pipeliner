# metainfo_magnet

Annotates entries whose URL is a magnet link with torrent metadata. URI-derived fields (info hash, tracker list, display name) are set immediately from the magnet URI itself. Full metadata (name, size, file list) is resolved via DHT when peers can be found within the configured timeout.

All DHT lookups for a batch of entries are dispatched in parallel, sharing a single timeout window, so a batch of 20 magnet links costs the same wall-clock time as a single one.

Entries with a non-magnet URL are silently skipped.

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
| `torrent_display_name` | string | Human-readable name from the `dn=` parameter |

### After DHT resolution (when peers respond within the timeout)

| Field | Type | Description |
|-------|------|-------------|
| `torrent_name` | string | Name from the info dict |
| `torrent_size` | int64 | Total size in bytes |
| `torrent_file_count` | int | Number of files |
| `torrent_files` | []string | File paths relative to the torrent root |

DHT resolution fields are absent when the timeout expires before peers respond. The entry is still passed to output plugins with only the URI-derived fields.

## Example

```yaml
tasks:
  magnets:
    rss:
      url: "https://example.com/rss/magnets"
    seen:
    metainfo_magnet:
      resolve_timeout: 45s
    content:
      require:
        - "*.mkv"
    quality:
      min: 720p
    qbittorrent:
      host: localhost
```

## Notes

- DHT resolution requires outbound UDP access to the BitTorrent DHT network. Entries with no reachable peers time out and retain only URI-derived fields.
- The plugin creates a single shared torrent client at plugin construction time. The client uses an ephemeral listen port and never uploads or seeds data.
- `resolve_timeout` applies to the entire batch, not per entry. Set it higher when processing large batches.
