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

## Example

```yaml
tasks:
  tv:
    rss:
      url: "https://example.com/feed"
    series:
      shows: ["Breaking Bad"]
    pathfmt:
      path: "/media/tv/{{.series_name}}/Season {{printf \"%02d\" .series_season}}"
    transmission:
      host: nas.local
      port: 9091
      username: admin
      password: secret
      path: "{{.download_path}}"
```
