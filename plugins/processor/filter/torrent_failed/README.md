# torrent_failed

Classifier for dead grabs: **accepts** torrents whose download has failed — errored in the client, or stalled/zero-progress for longer than `stall_timeout` — and **rejects** healthy ones. Upstream is the [`torrent_session`](../../source/torrent_session/README.md) source.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `stall_timeout` | duration | no | `4h` | How long a stalled/zero-progress torrent may sit without activity before it counts as failed |

## Classification rules (in order)

1. `torrent_state == "errored"` → **accepted** (reason includes the client's error message).
2. `torrent_state == "stalled"`, or `"downloading"` with `torrent_progress == 0`: inactivity is measured from `torrent_last_activity` (falling back to `torrent_added_at`). Once it reaches `stall_timeout` → **accepted**; before that → rejected.
3. Everything else (`seeding`, `paused`, `checking`, progressing downloads) → **rejected**.

A slow-but-moving download keeps refreshing its last-activity timestamp, so it is never classified as failed no matter how long it takes. A stalled torrent with no timing information at all is kept (rejected) rather than guessed at.

## Example

```python
sess   = input("torrent_session", backend="transmission")
failed = process("torrent_failed", upstream=sess, stall_timeout="4h")

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
