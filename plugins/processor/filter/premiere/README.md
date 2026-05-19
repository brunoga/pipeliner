# premiere

Accepts premiere episodes of previously unseen series, passing all
spec-matching quality variants downstream so the `dedup` processor can pick the
best copy. Useful for discovering new shows automatically: once a premiere is
successfully downloaded the series is recorded as seen and subsequent runs reject
it (hand off to `series` for ongoing episode tracking). If a download fails the
premiere is not recorded and will be retried on the next run.

State is persisted in `pipeliner.db` in the same directory as the config file.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `episode` | no | `1` | Episode number to treat as premiere |
| `season` | no | `1` | Season number to match; `0` means any season |
| `quality` | no | — | Quality spec the entry must satisfy (e.g. `720p+`, `webrip+`) |
| `reject_unmatched` | no | `true` | Reject entries whose titles do not parse as a series episode |

Episode metadata is parsed directly from the entry title — `metainfo_series` is not required.

See [`quality`](../quality/README.md) for the spec syntax.

## Fields set on entry

| Field | Description |
|-------|-------------|
| `series_episode_id` | Episode identifier (e.g. `S01E01`) |
| `series_season` | Season number |
| `series_episode` | Episode number |

These are the same fields produced by `metainfo_series`. A separate `metainfo_series` node is not required when `premiere` is already in the pipeline.

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
| May produce | `series_season`, `series_episode`, `series_episode_id` |
| Requires | — |
