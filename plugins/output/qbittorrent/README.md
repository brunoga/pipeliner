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

## Example

```yaml
tasks:
  movies:
    rss:
      url: "https://example.com/feed"
    movies:
      movies: ["Inception"]
      db: pipeliner.db
    pathfmt:
      path: "/media/movies/{{.movie_title}}"
    qbittorrent:
      host: localhost
      username: admin
      password: secret
      savepath: "{{.download_path}}"
      category: movies
```
