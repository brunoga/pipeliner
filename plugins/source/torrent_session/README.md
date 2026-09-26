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
| `torrent_file_size` | int | Total size of the torrent's data in bytes (same field the metainfo plugins set) |
| `torrent_downloaded` | int | All-time downloaded payload in bytes |
| `torrent_uploaded` | int | All-time uploaded payload in bytes |
| `torrent_download_rate` | int | Current download rate in bytes/second |
| `torrent_upload_rate` | int | Current upload rate in bytes/second |
| `torrent_connected_seeds` | int | Connected peers that have the complete torrent |
| `torrent_connected_peers` | int | Connected peers that do not |
| `torrent_error` | string | Client error message — only when `torrent_state` is `errored` |
| `torrent_last_activity` | time | Last transfer activity — only when the client reports one |
| `torrent_eta` | int | Estimated seconds to completion — only when the client can estimate one |
| `torrent_label` | string | Client-side label/category — only when set |
| `torrent_tracker_host` | string | Primary tracker host — only when the client reports one |
| `torrent_completed_at` | time | When the download finished — only once complete |

`torrent_connected_seeds` counts **live connections to this client**, not the
swarm size a tracker advertises. That is a different measurement from
`torrent_seeds`, which `rss` and `jackett` set from the indexer's seeder
column. On a torrent the janitor is about to purge, `torrent_connected_seeds`
is the field that says why: nothing is serving it.

Four of these depend on the backend knowing the answer, and are absent rather
than zero when it does not — so a template can tell "no estimate" from
"finishing now" with `{{with}}`. `torrent_eta` sentinels differ per client
(Transmission `-1`/`-2`, qBittorrent `8640000`) and are normalized away.
`torrent_label` needs the Label plugin enabled on Deluge.

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
| Produces | `title`, `source`, `torrent_info_hash`, `torrent_state`, `torrent_ratio`, `torrent_seed_time`, `torrent_added_at`, `torrent_progress`, `torrent_download_dir`, `torrent_file_size`, `torrent_downloaded`, `torrent_uploaded`, `torrent_download_rate`, `torrent_upload_rate`, `torrent_connected_seeds`, `torrent_connected_peers` |
| MayProduce | `torrent_error`, `torrent_last_activity`, `torrent_eta`, `torrent_label`, `torrent_tracker_host`, `torrent_completed_at` |
