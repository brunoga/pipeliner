# qbittorrent

Adds accepted torrents to a qBittorrent client via its Web API v2.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `host` | string | no | `localhost` | qBittorrent host |
| `port` | int | no | `8080` | Web UI port |
| `tls` | bool | no | false | Use HTTPS |
| `username` | string | no | — | Web UI username |
| `password` | string | no | — | Web UI password |
| `savepath` | string | no | `{{.download_path}}` | Download directory template |
| `category` | string | no | — | Torrent category |
| `tags` | string | no | — | Comma-separated tags |

## Error handling

If login fails or a torrent cannot be added, the affected entry is marked failed and will **not** be recorded by the learn phase. It will be retried on the next run.

## Grab records

Every successful add writes a grab record (shared `grabs` bucket, keyed by info-hash) linking the torrent back to its release URL and series/movies tracker keys, for [`mark_failed`](../mark_failed/README.md) failed-grab recovery. The qBittorrent add API does not return the hash, so it is taken from the entry's `torrent_info_hash` field (Jackett/RSS/metainfo plugins) or parsed from a magnet URL; a bare `.torrent` URL with neither has no record written.

## Example

```python
output("qbittorrent", upstream=ready,
       host="localhost", savepath="{download_path}")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
