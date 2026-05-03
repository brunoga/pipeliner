# Output plugins

Output plugins act on all accepted entries after filters, metainfo, and modify phases complete. A task can have multiple output plugins; all run for every accepted entry.

| Plugin | Description |
|--------|-------------|
| [transmission](transmission/README.md) | Add torrents to a Transmission client via JSON-RPC |
| [deluge](deluge/README.md) | Add torrents to a Deluge client via JSON-RPC |
| [qbittorrent](qbittorrent/README.md) | Add torrents to a qBittorrent client via Web API |
| [download](download/README.md) | Download the entry URL to a local file |
| [exec](exec/README.md) | Run a shell command for each accepted entry |
| [decompress](decompress/README.md) | Decompress downloaded archives (zip, rar, tar.gz, …) |
| [list_add](list_add/README.md) | Add accepted entries to a named persistent list |
| [email](email/README.md) | Send an email for each accepted entry |
| [print](print/README.md) | Print accepted entries to stdout |
| [notify](notify/README.md) | Delegate to a configured notify plugin |

## Download path

Torrent client plugins (`transmission`, `deluge`, `qbittorrent`) accept a `path` / `savepath` config key rendered against the entry field map. Combine with the `pathfmt` modify plugin to build structured media library paths:

```yaml
pathfmt:
  path: "/media/tv/{series_name}/Season {series_season:02d}"
transmission:
  host: localhost
  path: "{download_path}"
```

Go template syntax (`{{.field}}`) is also accepted for backward compatibility.
