# torrent_session

Emits one entry per torrent in a download client's session, so janitor pipelines can inspect ratio/seed-time/state and act via [`torrent_control`](../../sink/torrent_control/README.md) (remove/pause/reannounce) and [`mark_failed`](../../sink/mark_failed/README.md) (failed-grab recovery).

Supported backends: **Transmission** (JSON-RPC) and **qBittorrent** (Web API v2). Deluge is not supported yet.

## Config

Connection keys mirror the corresponding download sink's config:

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `backend` | string | yes | — | `transmission`, `qbittorrent`, or `deluge` |
| `host` | string | no | `localhost` | Client host |
| `port` | int | no | `9091` (transmission), `8080` (qbittorrent), `8112` (deluge) | Client port |
| `username` | string | no | — | Auth username |
| `password` | string | no | — | Auth password |
| `rpc_path` | string | no | `/transmission/rpc` | Transmission RPC endpoint path |
| `tls` | bool | no | `false` | Use HTTPS (qBittorrent only) |

## Fields set on entry

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Torrent display name |
| `source` | string | `torrent_session:<backend>` |
| `torrent_info_hash` | string | Lowercase hex info-hash (same field the metainfo plugins set) |
| `torrent_state` | string | Normalized state: `downloading`, `seeding`, `stalled`, `paused`, `errored`, `checking` |
| `torrent_ratio` | float | Upload ratio (negative client sentinels clamped to 0) |
| `torrent_seed_time` | int | Cumulative seeding time in seconds |
| `torrent_added_at` | time | When the torrent was added to the session |
| `torrent_progress` | float | Download completion percentage, 0–100 |
| `torrent_download_dir` | string | Directory the torrent's data lives in |
| `torrent_error` | string | Client error message — only when `torrent_state` is `errored` |
| `torrent_last_activity` | time | Last transfer activity — only when the client reports one |

The entry URL is the stable `torrent://<info-hash>`, so `dedup` and cross-branch matching by URL work.

### State normalization

| Normalized | Transmission | qBittorrent |
|------------|--------------|-------------|
| `errored` | `error != 0` | `error`, `missingFiles` |
| `stalled` | downloading with `isStalled` | `stalledDL` |
| `downloading` | download/download-wait | `downloading`, `metaDL`, `forcedDL`, `queuedDL` |
| `seeding` | seed/seed-wait | `uploading`, `stalledUP`, `forcedUP`, `queuedUP` |
| `paused` | stopped | `pausedDL/UP`, `stoppedDL/UP` |
| `checking` | check/check-wait | `checking*`, `moving`, `allocating` |

`stalledUP` (seeding with no leechers) is normal seeding, not a failure — only download-side stalls normalize to `stalled`.

## Example

```python
sess   = input("torrent_session", backend="transmission", host="localhost")
failed = process("torrent_failed", upstream=sess, stall_timeout="4h")
```

See `configs/torrent-janitor.star` for full session-janitor and failed-grab-recovery pipelines.

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `source`, `torrent_info_hash`, `torrent_state`, `torrent_ratio`, `torrent_seed_time`, `torrent_added_at`, `torrent_progress`, `torrent_download_dir` |
| MayProduce | `torrent_error`, `torrent_last_activity` |
