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

## Example

```yaml
tasks:
  movies:
    - rss:
        url: "https://example.com/feed"
    - movies:
        static: ["Inception"]
    - pathfmt:
        path: "/media/movies/{{.movie_title}}"
    - qbittorrent:
        host: localhost
        username: admin
        password: secret
        savepath: "{{.download_path}}"
        category: movies
```
