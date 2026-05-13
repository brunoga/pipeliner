# transmission

Adds accepted torrents to a Transmission BitTorrent client via its JSON-RPC API. Handles the session-id handshake (409 challenge/response) automatically.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `host` | string | no | `localhost` | Transmission host |
| `port` | int | no | `9091` | RPC port |
| `rpc_path` | string | no | `/transmission/rpc` | RPC endpoint path |
| `username` | string | no | — | HTTP basic auth username |
| `password` | string | no | — | HTTP basic auth password |
| `path` | string | no | `{{.download_path}}` | Download directory template |
| `paused` | bool | no | false | Add torrent in paused state |

## Error handling

If a torrent cannot be added, the affected entry is marked failed and will **not** be recorded by the learn phase. It will be retried on the next run.

## Example

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen", upstream=src)
series = process("series", upstream=seen, static=["Breaking Bad"])
output("transmission", upstream=series,
       host="localhost", port=9091)
pipeline("tv", schedule="30m")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
