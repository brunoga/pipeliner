# torrent_failed

Classifier for dead grabs: **accepts** torrents whose download has failed — errored in the client, or stalled/zero-progress for longer than `stall_timeout` — and **rejects** healthy ones, including any torrent that already reached 100%. Upstream is the [`torrent_session`](../../source/torrent_session/README.md) source.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `stall_timeout` | duration | no | `4h` | How long a stalled/zero-progress torrent may sit without activity before it counts as failed |
| `max_eta` | duration | no | off | Fail a download that, at the best of its lifetime, recent and current rates, would still take longer than this to finish |
| `slow_window` | duration | no | `24h` | With `max_eta`: how long a download is watched before it may be failed for being slow, and the span its recent rate is measured over |

## Classification rules (in order)

1. `torrent_progress >= 100` → **rejected** (`healthy (complete)`), whatever the state says. A finished torrent has already delivered its data, so the janitor's sinks would both do damage: `torrent_control(action="remove_with_data")` would delete the completed download, and `mark_failed` would un-track content that is already in the library — which re-downloads it on the next run. A complete torrent errors for reasons that say nothing about the download (the moved files are no longer where the client expects them, the tracker went away, seeding hit an I/O error), which is why this check precedes the errored rule rather than following it.
2. `torrent_state == "errored"` → **accepted** (reason includes the client's error message).
3. `torrent_state == "stalled"`, or `"downloading"` with `torrent_progress == 0`: inactivity is measured from `torrent_last_activity` (falling back to `torrent_added_at`). Once it reaches `stall_timeout` → **accepted**; before that → rejected.
4. With `max_eta` set, a `stalled` or `downloading` torrent that has been watched for a whole `slow_window`, and whose best rate still leaves more than `max_eta` to go → **accepted** (`too slow: …`, with the three rates).
5. Everything else (`seeding`, `paused`, `checking`, progressing downloads) → **rejected**.

A slow-but-moving download keeps refreshing its last-activity timestamp, so `stall_timeout` never fails it no matter how long it takes. A stalled torrent with no timing information at all is kept (rejected) rather than guessed at.

## Trickles: `max_eta`

A swarm serving a few KB/s keeps the last-activity time fresh while the download would take months. With `max_eta` set, every run stores a sample of each downloading torrent's all-time downloaded counter in the `torrent_failed_rates` bucket, keyed by info-hash, and judges the torrent on the **best** of three rates:

- **lifetime:** downloaded bytes over the time since it was added
- **recent:** bytes fetched since the sample `slow_window` ago
- **current:** the client's rate right now

It is failed only when even that best rate leaves more than `max_eta` to go, and only once it has been watched downloading for a whole `slow_window`. That is what keeps one slow reading from failing a torrent: a torrent that was fast, sped up recently, or is fast now is kept. The watch must be continuous. A torrent that is paused, queued, seeding or gone from the session has its samples dropped, and so does a re-added one whose counter went backwards; its watch starts again.

## Example

```python
sess   = input("torrent_session", backend="transmission")
failed = process("torrent_failed", upstream=sess, stall_timeout="4h", max_eta="96h")

# Fan out: record the failure, then purge the dead torrent and its data.
output("mark_failed", upstream=failed)
output("torrent_control", upstream=failed,
       action="remove_with_data", backend="transmission")
```

See `configs/torrent-janitor.star` and the [`mark_failed`](../../../sink/mark_failed/README.md) README for how the failure is recorded and retried.

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | `torrent_state` |
