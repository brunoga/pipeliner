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

## Example

```yaml
tasks:
  tv:
    rss:
      url: "https://example.com/feed"
    series:
      shows: ["Breaking Bad"]
      db: pipeliner.db
    pathfmt:
      path: "/media/tv/{{.series_name}}/Season {{printf \"%02d\" .series_season}}"
    deluge:
      host: localhost
      password: changeme
      path: "{{.download_path}}"
```
