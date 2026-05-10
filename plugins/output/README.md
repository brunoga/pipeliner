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

## Error handling and retries

Output plugins fall into two categories with different failure semantics:

**Downloader plugins** (`transmission`, `deluge`, `qbittorrent`, `download`) mark an entry as **failed** when they cannot queue or fetch it. A failed entry is not recorded by the learn phase, so it will be picked up and retried on the next run.

**Notification plugins** (`email`, `exec`, `print`, `notify`) do not mark entries as failed on error. The entry is still considered processed — a notification failure is not a reason to re-download.

## Download path

Torrent client plugins (`transmission`, `deluge`, `qbittorrent`) accept a `path` / `savepath` config key rendered against the entry field map. Combine with the `pathfmt` modify plugin to build structured media library paths:

```python
plugin("pathfmt", path="/media/tv/{series_name}/Season {series_season:02d}", field="download_path"),
plugin("transmission", host="localhost", path="{download_path}"),
```

Go template syntax (`{{.field}}`) is also accepted for backward compatibility.
