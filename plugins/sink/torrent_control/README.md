# torrent_control

Acts on torrents in a download client's session: remove them, pause them, or force a tracker reannounce. Pairs with the [`torrent_session`](../../source/torrent_session/README.md) source, which emits the `torrent_info_hash` field this sink requires.

> **⚠️ `remove_with_data` deletes the torrent's downloaded files from disk**, not just the session entry. There is no undo. Use plain `remove` to keep the files.

## Config

Connection keys mirror `torrent_session`:

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `action` | string | yes | — | `remove`, `remove_with_data`, `pause`, or `reannounce` |
| `backend` | string | yes | — | `transmission`, `qbittorrent`, or `deluge` |
| `host` | string | no | `localhost` | Client host |
| `port` | int | no | `9091` (transmission), `8080` (qbittorrent), `8112` (deluge) | Client port |
| `username` | string | no | — | Auth username |
| `password` | string | no | — | Auth password |
| `rpc_path` | string | no | `/transmission/rpc` | Transmission RPC endpoint path |
| `tls` | bool | no | `false` | Use HTTPS (qBittorrent only) |

## Behavior

- Requires `torrent_info_hash`; entries without it are marked failed.
- Hashes are batched into a single client call per run.
- Dry-run logs `would remove/pause/reannounce <torrent>` previews and makes **zero** client calls.
- On success entries get an accept reason (`torrent_control: removed <title>`), so a chained `notify` sink fires only after the client confirmed the operation.
- `pause` uses Transmission's `torrent-stop`; on qBittorrent it tries `torrents/pause` and falls back to `torrents/stop` (the qBittorrent 5.x rename).

## Example

```python
# Remove torrents that are done seeding.
sess = input("torrent_session", backend="transmission")
done = process("condition", upstream=sess, rules=[
    {"accept": 'torrent_state == "seeding" and torrent_ratio >= 2.0'},
    {"reject": "true"},
])
output("torrent_control", upstream=done, action="remove", backend="transmission")
```

See `configs/torrent-janitor.star` for the full janitor pipelines.

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `torrent_info_hash` |
