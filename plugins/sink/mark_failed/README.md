# mark_failed

Records a dead torrent's **original release URL** in the shared failed-grab bucket (`seen_failed`) and un-tracks the associated episode/movie, so a *different* release of the same content can be grabbed on a later run while the exact failed release is never retried. Typically fed by [`torrent_failed`](../../processor/filter/torrent_failed/README.md).

## How the hash → release-URL linkage works

Session entries only carry the torrent info-hash (their URL is `torrent://<hash>`), not the release URL the seen filter and trackers know about. To bridge that gap, the **transmission and qbittorrent sinks write a grab record at add time** — a shared `grabs` bucket entry keyed by info-hash containing the release URL plus the series/movies tracker keys the upstream filters stamped:

- **transmission** gets the hash from the `torrent-add` RPC response, so every successful add is recorded.
- **qbittorrent**'s add API does not return the hash, so it comes from the entry's `torrent_info_hash` field (set by `metainfo_torrent`/`metainfo_magnet`/Jackett/RSS) or is parsed from a magnet URL. A bare `.torrent` URL that never went through a metainfo plugin has no locally determinable hash — no record is written and this sink cannot resolve that torrent later.

Entries whose hash has **no grab record** (added outside pipeliner, added before grab recording existed, or the qBittorrent case above) are marked failed with a clear reason and nothing is written.

## What one successful mark does

1. Puts the release URL into `seen_failed` with `{reason, failed_at}`. A [`seen`](../../processor/filter/seen/README.md) filter configured with `retry_failed=True` rejects that exact URL forever.
2. Forgets the episode in the series tracker (or the movie in the movies tracker), so the `series`/`movies` filters stop treating the content as downloaded and an alternative release passes on the next run. Grab records without tracker keys (pipelines with no series/movies filter) skip this step.
3. Deletes the consumed grab record.

The recorded reason is the entry's accept reason (as stamped by `torrent_failed`, e.g. `torrent_failed: errored: tracker unregistered`), overridable via the `reason` key.

Note: entries that fail **at add time** (the download sink itself failed the entry) need none of this — the commit phase never runs for them, so the seen filter and trackers were never updated and the next run retries naturally. This sink exists for grabs that were confirmed at add time but died later.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `reason` | string | no | entry's accept reason, else `grab failed` | Failure reason recorded with the URL |

## Behavior

- Requires `torrent_info_hash`; entries without it are marked failed.
- Dry-run logs `would mark failed: <url>` previews and makes **no** store writes.
- On success entries get an accept reason (`mark_failed: marked failed: <url> (<reason>)`), so a chained `notify` fires only after the write happened.

## Example

```python
sess   = input("torrent_session", backend="transmission")
failed = process("torrent_failed", upstream=sess)
output("mark_failed", upstream=failed)
output("torrent_control", upstream=failed,
       action="remove_with_data", backend="transmission")
```

And in your download pipelines, enable the failed-URL check:

```python
seen = process("seen", upstream=src, retry_failed=True)
```

See `configs/torrent-janitor.star` for the complete flow.

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `torrent_info_hash` |
