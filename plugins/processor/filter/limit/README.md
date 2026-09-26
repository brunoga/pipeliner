# limit

Caps the number of accepted entries flowing through the pipeline to `n`,
rejecting the rest. Useful when a source (especially a `discover` node) yields
more candidates than you actually want to download in a single run — for
example, manually-triggered pipelines that go wide on a watchlist.

Without `sort`, the first `n` accepted entries in arrival order win. With
`sort`, entries are ordered by the named field and the top `n` win.

## Config

| Key | Required | Default | Description |
|---|---|---|---|
| `n` | yes | — | Maximum number of accepted entries to forward (>= 1) |
| `sort` | no | — | Entry field to sort by; absent means arrival order |
| `order` | no | `desc` | Sort direction when `sort` is set: `desc` or `asc` |

Entries missing the `sort` field are bucketed last regardless of `order`, so
they never beat an entry that has the field.

## Sort value types

Numeric (`int`, `int64`, `float64`), strings, `time.Time`, and booleans are
supported. Numeric types compare across each other (e.g. `int` vs `float64`);
other type mismatches fall back to arrival order for the pair.

String-encoded dates (RFC3339, RFC822, `2006-01-02`, `Jan 2, 2006`, and a few
more) are promoted to `time.Time` before comparison, so `published_date` from
RSS feeds sorts chronologically.

## DAG role

| Property | Value |
|---|---|
| Role | `processor` |
| Produces | — |
| Requires | — (sort field is optional and missing values are tolerated) |
| InputStates | `accepted` only — undecided entries are not candidates yet, rejected and failed ones are terminally decided |

## Placement: put it last

The cap applies to whatever is accepted **at that point in the graph**, so every
rejecting filter belongs *upstream* of it. A filter placed downstream spends
slots on entries that will never reach the sink, and the run downloads fewer
than `n`.

The case that bites is a [`seen`](../seen/) filter after the cap. With
`retry_failed=True` it rejects releases in the shared failed-grab bucket — but
[`mark_failed`](../../../sink/mark_failed/) deliberately **un-tracks** the
episode or movie when it blocklists a release, so a different release can be
grabbed later. That un-tracking makes the blocklisted release invisible to the
[`movies`](../movies/)/[`series`](../series/) filter upstream, so it is accepted,
reaches the cap, takes a slot, and is only then rejected by `seen`.

Nothing upstream removes it, so it takes that slot on **every** run. With a
small `n` a handful of such releases starves the pipeline to zero downloads
indefinitely.

Move `seen` above `limit` — it defaults to `InputStates: accepted+undecided`, so
it works before anything has accepted. Keep it after whatever sets
`torrent_info_hash` (usually [`metainfo_torrent`](../../metainfo/torrent/)):
indexer download URLs rotate per search, so the info hash is what actually
matches. Doing this also skips the metadata lookups those entries would have
consumed.

## Examples

Cap a manual 3D-discovery pipeline to 5 movies per run, picking the highest
rated:

```python
src    = input("trakt_list", list="watchlist", access_token=env("TRAKT_TOKEN"))
disc   = process("discover", upstream=src, search=["jackett"], match=True)
meta   = process("metainfo_file", upstream=disc)
q3d    = process("condition", upstream=meta,
                accept=[{"name": "3d", "expr": "video_is_3d"}])
ext    = process("metainfo_tmdb", upstream=q3d, api_key=env("TMDB_KEY"))
top5   = process("limit", upstream=ext, n=5, sort="video_rating")
output("transmission", upstream=top5, host="localhost")
pipeline("3d-movies")  # manual trigger only
```

Take only the 10 newest entries from a high-volume feed:

```python
recent = process("limit", upstream=src, n=10, sort="published_date")
```

First-N order (no sort) — useful when upstream order is already meaningful:

```python
top = process("limit", upstream=ranked, n=3)
```
