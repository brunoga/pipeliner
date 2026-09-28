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

## Required ordering: dedup goes last among the refusals

`dedup` keeps one release per item and discards the rest, choosing on quality
tags because that is all it has. **Every node that can refuse an individual
release must therefore run above it** — `content`, `bitrate`, `require`, a
`condition` on a per-release field, `metainfo_torrent` (which fails the entry
when the indexer link has expired). Placed below, the alternatives are already
gone when the refusal lands: the item is lost for that run, and for every run
after it, because the same wave comes back and collapses to the same winner.

```python
# Wrong — content refuses the only release left
dd   = process("dedup",   upstream=rate)
cont = process("content", upstream=dd, reject=["*.rar"])

# Right — content thins the wave, dedup picks the best survivor
cont = process("content", upstream=rate, reject=["*.rar"])
dd   = process("dedup",   upstream=cont)
```

`pipeliner check` and the visual editor report this for you: the descriptor
carries `Collapses: true`, every processor declares a `Refusal` kind, and
`dag.Validate` warns when a per-release refusal sits below a collapsing node
with no other route to a sink. See the user guide, *Node Ordering*.

Refusals that hold for the whole item are fine below `dedup` — `seen`
("already downloaded") and `limit` ("enough items this run") would refuse the
alternatives for the same reason.

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
