# premiere

Accepts premiere episodes of previously unseen series, passing all
spec-matching quality variants downstream so the `dedup` processor can pick the
best copy. Useful for discovering new shows automatically: once a premiere is
successfully downloaded the series is recorded as seen and subsequent runs reject
it (hand off to `series` for ongoing episode tracking). If a download fails the
premiere is not recorded and will be retried on the next run.

State is persisted in `pipeliner.db` in the same directory as the config file.
The shared tracker bucket is the same one used by the `series` plugin — a show
downloaded via `premiere` will not be re-offered to `series` and vice versa.

## Upstream requirement

`premiere` reads episode metadata from entry fields; it does **not** parse the
title itself. You must run [`metainfo_file`](../../metainfo/file/README.md) (or
any equivalent plugin that sets the same fields) upstream:

| Field | Used for |
|-------|----------|
| `title` | Show name (normalized for the tracker key) |
| `series_episode_id` | Tracker key + classification gate |
| `series_season` | Season constraint check |
| `series_episode` | Episode constraint check |
| `_quality` *(via `e.Quality()`)* | Quality spec matching, persisted record |
| `series_double_episode` *(optional)* | Marks each part of a double episode |

The first five fields are declared via `Descriptor.Requires`, so the DAG
validator catches pipelines that wire `premiere` without an upstream
metainfo step.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `episode` | no | `1` | Episode number to treat as premiere |
| `season` | no | `1` | Season number to match; `0` means any season |
| `quality` | no | — | Quality spec (e.g. `720p+` for floor, `720p` for exact, `720p-1080p` for range) |
| `reject_unmatched` | no | `true` | Reject entries that lack `series_episode_id` (i.e. were not classified as a series episode upstream). Set `false` to leave them undecided when chaining filters. |

See [`quality`](../quality/README.md) for the spec syntax.

## Example

```python
src  = input("rss", url="https://example.com/rss")
seen = process("seen",          upstream=src)
meta = process("metainfo_file", upstream=seen)   # sets series_*, _quality, etc.
prem = process("premiere",      upstream=meta, quality="720p+ webrip+")
best = process("dedup",         upstream=prem)
output("transmission", upstream=best, host="localhost")
pipeline("new-shows", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Requires | `title`, `series_episode_id`, `series_season`, `series_episode`, `_quality` (declared via `RequireAll`) |
| Produces | — (no new public fields; reads-only of upstream metadata) |

`premiere` passes through fields set upstream; it does not produce new ones.
The same `series_*` fields are available downstream because `metainfo_file`
(upstream) already set them.

## Notes

- Episode history is stored in `pipeliner.db` in the same directory as the config file.
- The episode tracker is updated only after all downstream sinks confirm (via `CommitPlugin`). If a sink fails an entry, the series is not recorded as seen and will be retried on the next run.
- **Double episodes** (e.g. `S01E01E02`): when a double-episode premiere is committed, both individual episodes (`S01E01` and `S01E02`) are also marked as seen, preventing re-download of either part as a standalone release later.
