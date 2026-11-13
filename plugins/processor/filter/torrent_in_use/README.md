# torrent_in_use

Rejects files that a download client's torrent still uses. It is the guard in front of an age-based file sweep: deleting a file a torrent is seeding from stops the seeding, and on a tracker that enforces a share ratio that costs the account. Leave those files to whatever removes the torrent (`torrent_control` with `remove_with_data`).

A torrent uses everything below its download directory joined with its name: the file of a single-file torrent, the folder of a multi-file one. The client is asked once per run. **When it cannot be asked, every entry is rejected**: deleting a file that might be seeding is the mistake this filter exists to prevent, so not knowing means not deleting.

The paths must be the ones the client sees. When pipeliner runs in a container, mount the media folders at the same paths the download client uses.

## Config

Connection keys mirror `torrent_session` and `torrent_control`:

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `backend` | yes | — | `transmission`, `qbittorrent`, or `deluge` |
| `host` | no | `localhost` | Client host |
| `port` | no | `9091` / `8080` / `8112` | Client port (default depends on backend) |
| `username` | no | — | Auth username |
| `password` | no | — | Auth password |
| `rpc_path` | no | `/transmission/rpc` | Transmission RPC endpoint path |
| `tls` | no | `false` | Use HTTPS (qBittorrent only) |

## Example

```python
files = input("filesystem", path="/data/media", recursive=True)
old   = process("condition", upstream=files, rules=[
    {"accept": "file_modified_time < daysago(7)"},
    {"reject": "true"},
])
free  = process("torrent_in_use", upstream=old, backend="deluge", password=env("DELUGE_PASSWORD"))
output("delete", upstream=free, roots=["/data/media/series", "/data/media/movies"])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | `file_location` |
