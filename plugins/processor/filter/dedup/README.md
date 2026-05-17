# dedup

Removes duplicate entries for the same media item, keeping the best-quality copy.
Place it after `series` or `movies` (which accept all quality variants of the same
episode/movie) and before output sinks.

## Selection priority

1. **Seed tier** — entries with 2+ seeds beat entries with exactly 1 seed
2. **Resolution** — higher resolution wins within the same seed tier
3. **Seeds** — more seeds wins when tier and resolution are equal

Episodes are keyed by series title (case-insensitive) + episode ID; movies by movie title (case-insensitive).
Entries without either key pass through unchanged.

## Config

No configuration options.

```python
process("dedup")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — (entries without `series_episode_id` or `movie_title` pass through unchanged) |

## Example

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",   upstream=src)
series = process("series", upstream=seen, static=["Breaking Bad"])
q      = process("metainfo_quality", upstream=series)
dd     = process("dedup",  upstream=q)
output("transmission", upstream=dd, host="localhost")
pipeline("tv", schedule="30m")
```
