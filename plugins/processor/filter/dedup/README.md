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
| Requires | `media_type` AND (`series_episode_id` OR `title`). Place `metainfo_file`, `metainfo_tmdb`, or `metainfo_tvdb` upstream so `media_type` is set; entries lacking it pass through unchanged. |

## Example

```python
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",   upstream=src)
meta   = process("metainfo_file", upstream=seen)
series = process("series", upstream=meta, static=["Breaking Bad"])
q      = process("metainfo_file", upstream=series)
dd     = process("dedup",  upstream=q)
output("transmission", upstream=dd, host="localhost")
pipeline("tv", schedule="30m")
```
