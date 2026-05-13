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
