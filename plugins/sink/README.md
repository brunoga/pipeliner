# Sink plugins (`plugins/sink/`)

Sink plugins act on accepted entries. Use them with `output("plugin-name", upstream=…)`.
Call `output()` multiple times from the same upstream node for fan-out.

| Plugin | Description |
|--------|-------------|
| [`transmission`](transmission/README.md) | Add torrents to a Transmission client via JSON-RPC |
| [`deluge`](deluge/README.md) | Add torrents to a Deluge client via JSON-RPC |
| [`qbittorrent`](qbittorrent/README.md) | Add torrents to a qBittorrent client via Web API |
| [`download`](download/README.md) | Download the entry URL to a local file |
| [`exec`](exec/README.md) | Run a shell command for each accepted entry |
| [`decompress`](decompress/README.md) | Decompress downloaded archives (zip, rar, 7z) |
| [`list_add`](list_add/README.md) | Add accepted entries to a named persistent list |
| [`email`](email/README.md) | Send an email for each batch of accepted entries |
| [`print`](print/README.md) | Print accepted entries to stdout |
| [`notify`](notify/README.md) | Delegate to a configured notifier (webhook, email, Pushover) |

## Notifier sub-plugins (`plugins/sink/notify/`)

Used inside the `notify` sink via the `via` config key.

| Plugin | Description |
|--------|-------------|
| [`email`](notify/email/README.md) | Send a run-summary email via SMTP |
| [`pushover`](notify/pushover/README.md) | Send a notification via the Pushover API |
| [`webhook`](notify/webhook/README.md) | POST a run summary to an HTTP endpoint |
