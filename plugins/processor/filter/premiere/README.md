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

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `episode` | no | `1` | Episode number to treat as premiere |
| `season` | no | `1` | Season number to match; `0` means any season |
| `quality` | no | — | Quality spec (e.g. `720p+` for floor, `720p` for exact, `720p-1080p` for range) |
| `reject_unmatched` | no | `true` | Reject entries whose titles do not parse as a series episode; set `false` to leave them undecided when chaining filters |

Episode metadata is parsed directly from the entry title — `metainfo_series` is not required.

See [`quality`](../quality/README.md) for the spec syntax.

## Fields set on entry

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Parsed series name (e.g. `Breaking Bad`) |
| `series_season` | int | Season number |
| `series_episode` | int | Episode number |
| `series_episode_id` | string | Episode identifier (e.g. `S01E01`, `S01E01E02` for doubles) |
| `series_double_episode` | int | Second episode number for double-episode releases |
| `series_proper` | bool | `true` if the release is a PROPER |
| `series_repack` | bool | `true` if the release is a REPACK |
| `series_service` | string | Streaming service tag (e.g. `AMZN`, `NF`) |
| `series_container` | string | Container format tag if present (e.g. `mkv`) |

These are the same fields produced by `metainfo_series`; a separate `metainfo_series` node is not required when `premiere` is already in the pipeline.

## Example

```python
src  = input("rss", url="https://example.com/rss")
seen = process("seen",     upstream=src)
prem = process("premiere", upstream=seen, quality="720p+ webrip+")
best = process("dedup",    upstream=prem)
output("transmission", upstream=best, host="localhost")
pipeline("new-shows", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| May produce | `title`, `series_season`, `series_episode`, `series_episode_id`, `series_double_episode`, `series_proper`, `series_repack`, `series_service`, `series_container` |
| Requires | — |

## Notes

- Episode history is stored in `pipeliner.db` in the same directory as the config file.
- The episode tracker is updated only after all downstream sinks confirm (via `CommitPlugin`). If a sink fails an entry, the series is not recorded as seen and will be retried on the next run.
- **Double episodes** (e.g. `S01E01E02`): when a double-episode premiere is committed, both individual episodes (`S01E01` and `S01E02`) are also marked as seen, preventing re-download of either part as a standalone release later.
- `metainfo_series` is not needed in pipelines that already include `premiere` — all the same fields are set by the `premiere` filter itself.
