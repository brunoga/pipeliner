# deluge

Adds accepted torrents to a Deluge client via its Web UI JSON-RPC API.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `host` | string | no | `localhost` | Deluge Web UI host |
| `port` | int | no | `8112` | Web UI port |
| `password` | string | yes | — | Web UI password |
| `tls` | bool | no | false | Use HTTPS |
| `path` | string | no | `{{.download_path}}` | Download directory template |

## Error handling

If login fails or a torrent cannot be added, the affected entry is marked failed and will **not** be recorded by the learn phase. It will be retried on the next run.

## Failed-grab recovery

Every successful add writes a grab record (shared `grabs` bucket, keyed by info-hash) linking the torrent back to its release URL and series/movies tracker keys, for [`mark_failed`](../mark_failed/README.md) failed-grab recovery. The Deluge Web UI add API does not return the hash, so it is taken from the entry's `torrent_info_hash` field (Jackett/RSS/metainfo plugins) or parsed from a magnet URL; a bare `.torrent` URL with neither has no record written.

## Example

```python
output("deluge", upstream=ready,
       host="localhost", password=env("DELUGE_PASS"),
       move_completed_path="{download_path}")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
