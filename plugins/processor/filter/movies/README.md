# movies

Accepts movies. Optionally matches against a title list with fuzzy matching and persists download history across runs. A re-download of an already-seen movie is accepted when the new copy is strictly better quality, or when it is a PROPER/REPACK that is not a quality downgrade.

Quality-spec gating is now a separate concern — place a `process("quality", spec=…)` node upstream of `movies` when you want a quality floor; the per-plugin `quality=` knob has been removed.

**Multiple quality variants** of the same movie (from different sources or input feeds) are all accepted; add a `dedup` node after `movies` to keep only the best copy.

**3D and non-3D versions are tracked independently.** If both a 3D and a non-3D copy of the same movie match, both are downloaded — they do not compete with each other.

The title list is optional. When provided, it can be static via `static`, dynamic via `list` (input plugins whose entry titles are used as movie titles), or both — dynamic results are cached for the configured `ttl`. **With neither set, the filter operates in accept-all mode**: every classified movie passes the upstream requirements and the tracker checks. The tracker still dedups and detects upgrades by title + year, so a no-list config is the right choice for "download every movie I find" pipelines.

## Upstream requirement

`movies` reads movie metadata from entry fields; it does **not** parse the title itself. You must run [`metainfo_file`](../../metainfo/file/README.md) (or any equivalent plugin that sets the same fields) upstream:

| Field | Used for |
|-------|----------|
| `title` | Movie title (matched against the configured list) |
| `video_year` | Year-aware matching + tracker key |
| `_quality` *(via `e.Quality()`)* | Quality spec matching, upgrade comparison, persisted record |
| `video_is_3d` *(optional)* | Tracks 3D and non-3D versions independently |
| `video_proper`, `video_repack` *(optional)* | PROPER/REPACK upgrade detection |

`title`, `video_year`, and `_quality` are declared via `Descriptor.Requires`, so the DAG validator catches pipelines that wire `movies` without an upstream metainfo step.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `static` | string or list | no | — | Optional static movie titles to accept; omit (alongside `list`) for accept-all mode |
| `list` | list | no | — | Optional list-plugin configs whose entry titles supplement the movie list; omit (alongside `static`) for accept-all mode |
| `ttl` | string | no | `1h` | How long to cache the dynamic list fetched via `list` |
| `reject_unmatched` | bool | no | `true` | Reject entries that lack `title`. When a list is configured, also reject entries whose title isn't in the list. With neither `static` nor `list` set, this flag only governs the classification check. |

Both `static` and `list` are optional. With neither set the filter accepts every classified movie that passes the tracker checks. For a quality floor, place a [`quality`](../quality/README.md) filter upstream.

### `list` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config:

```python
q      = process("quality", upstream=meta, spec="1080p+")
movies = process("movies",  upstream=q,
    list=[{"name": "trakt_list", "client_id": env("TRAKT_ID"),
           "client_secret": env("TRAKT_SECRET"), "type": "movies", "list": "watchlist"}])
```

## 3D quality

3D format is a ranked quality dimension. When two 3D copies of the same movie are compared, the 3D format rank takes precedence over resolution, source, and all other dimensions; those become tie-breakers.

| Rank | Format | Detected markers |
|------|--------|-----------------|
| Lowest | Half | `3D`, `HSBS`, `H-SBS`, `HALF-SBS`, `HOU`, `H-OU`, `HALF-OU` |
| Middle | Full | `SBS`, `FSBS`, `F-SBS`, `FULL-SBS`, `OU`, `FOU`, `F-OU`, `FULL-OU` |
| Highest | BD | `BD3D` |

Plain `3D` without a subtype is treated as Half (most common encoding for generic 3D releases).

The 3D format is included in the `video_quality` string (e.g. `BD3D 1080p BluRay H.265`).

3D and non-3D versions of the same movie are tracked independently — both are downloaded if both match.

Filtering out 3D releases entirely:

```python
cond = process("condition", upstream=movies, reject="video_is_3d == true")
```

## Debug logging

Run with `--log-level debug --log-plugin movies` to see (combine plugins with a comma, e.g. `--log-plugin movies,metainfo_tmdb`):
- Which titles are loaded from `list` sources (cache hit or live fetch)
- Why individual entries are skipped (title not in list, missing metadata)

## Example — static list

```python
src    = input("rss",            url="https://example.com/rss")
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)   # sets title, video_year, _quality, etc.
q      = process("quality",       upstream=meta, spec="1080p+")
movies = process("movies",        upstream=q, static=["Inception"])
output("qbittorrent", upstream=movies, host="localhost")
pipeline("movies", schedule="1h")
```

## Example — dynamic list from Trakt watchlist

```python
src    = input("rss",            url="https://example.com/rss")
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)
q      = process("quality",       upstream=meta, spec="1080p+")
movies = process("movies",        upstream=q,
    list=[{"name": "trakt_list", "client_id": env("TRAKT_ID"),
           "client_secret": env("TRAKT_SECRET"), "type": "movies", "list": "watchlist"}])
output("qbittorrent", upstream=movies, host="localhost")
pipeline("movies-trakt", schedule="1h")
```

To accept only BD3D quality or better among 3D releases (and still download non-3D copies independently), use the `video_quality` field which includes the 3D format string:

```python
cond = process("condition", upstream=movies, rules=[
    {"reject": 'video_is_3d == true and not contains(video_quality, "BD3D")'},
])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Requires | `title`, `video_year`, `_quality` (declared via `RequireAll`) |
| Produces | `media_type` (= `"movie"`) |

`movies` is a classifier by construction — every entry it lets through is a movie — so it stamps `media_type = "movie"` on each processed entry. Downstream nodes (e.g. `dedup`, `route`, `condition`) can rely on the field being present as Certain, instead of inheriting `metainfo_file`'s conditional `MayProduce`. The `video_*` fields are available downstream because `metainfo_file` (upstream) already set them.

## Notes

- Download history and dynamic list cache are stored in `pipeliner.db` in the same directory as the config file.
- The tracker is updated only after all downstream sinks confirm (via `CommitPlugin`). If a sink fails an entry, the movie is not recorded as downloaded and will be retried on the next run.
- **Year-drift tolerance.** The tracker key is `(title, year, is3D)`. To survive theatrical-vs-Blu-ray release-year drift (e.g. *Good Boy*: 2025 theatrical / 2026 Blu-ray), `IsSeen` falls back to a ±1 year scan when the exact key misses, and the upgrade decision uses the same window. Two films sharing a title that are ≥2 years apart are still treated as independent (e.g. *The Matrix* 1999 vs. a 2021 reboot).
