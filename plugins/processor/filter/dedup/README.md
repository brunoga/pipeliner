# dedup

Removes duplicate entries for the same media item, keeping the best-quality copy.
Place it after `series` or `movies` (which accept all quality variants of the same
episode/movie) and before output sinks.

## Selection priority

1. **Seed tier** — entries with 2+ seeds beat entries with exactly 1 seed
2. **Quality** — `quality.Better`: the whole ladder, `Resolution > Source > Codec > ColorRange > Audio` (3D format first when both releases are 3D)
3. **Seeds** — more seeds wins when tier and quality are equal

Step 2 used to read the resolution and nothing else, which made every other rung invisible here: a BluRay tied a remux, a WEB-DL tied a BluRay, a plain copy tied an Atmos one — and a tie falls through to seeds and then to the order the indexer happened to return. A request for *Supergirl (2026)* came back with both `Complete 4K UHD Blu Ray ISO File` and `2160p UHD BluRay REMUX DV HDR TrueHD Atmos`, both 2160p, both 6 seeds, and the 90 GB disc image won on list order. It is the same comparator the [`library`](../library/) filter and the upgrade check use, so "better" now means one thing across the codebase.

Quality is read from the typed `_quality` field the pipeline already gated on, falling back to parsing the release name where nothing set it.

**Seed tier stays ahead of quality on purpose.** A release with one seeder is a download that may never finish, and the best copy you cannot get is not the best copy.

Episodes are keyed by normalized series title without a trailing year + episode ID (`Brothers 2026 S01E01` and `Brothers S01E01 2026` are one episode); movies by movie title (case-insensitive).
Entries without either key pass through unchanged.

## A shared name is not a shared item

Titles are not unique. TheTVDB lists two different series called *Tomb Raider*; there is a *Dune* from 1984 and one from 2021. Grouping by name alone meant the older film was rejected as "a better copy" of the newer one and the request for it came back with the wrong film — and for episodes, where the name key has the year stripped on purpose, nothing at all separated two same-named shows.

So entries sharing a name key are treated as copies of one another only when nothing *proves* they are different:

| Signal | Effect |
|---|---|
| Provider ids that disagree (`tvdb_id`, `tmdb_id`, IMDb) | different items |
| Release years more than one apart (movies) | different items |
| An id present on one copy and missing on the other | nothing proven — still copies |
| Ids from different namespaces (one TMDB, one IMDb) | nothing proven — still copies |

Absence never splits a group, and that matters more than it looks: most releases publish no id, a metainfo plugin leaves an entry unenriched rather than guess, so a copy with an id beside a copy without is the normal case. Splitting on absence would stop dedup working for exactly those and download the same episode twice.

The id is read from what pipeliner resolved itself first (`tvdb_id`, `tmdb_id`), then a list provider's metadata (`trakt_*_id`), then the id an indexer published with the release (`jackett_*_id`) — that last is a claim about a file rather than an established identity, so it loses to the others, and here it can only ever separate two copies that share a title, never merge two that do not.

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
