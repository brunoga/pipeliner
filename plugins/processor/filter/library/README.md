# library

Rejects entries whose episode or movie already exists in the actual media
library on disk at equal-or-better quality. Unlike [`seen`](../seen/README.md)
(which knows what pipeliner grabbed), this checks **disk truth** — it catches
content acquired outside pipeliner, and it enables real quality upgrades: a
release strictly better than the library copy passes through (disable with
`upgrade=false`).

With `backend="plex"` and **no** `url`/`token`, the filter runs in **account mode**: it uses the Settings → Plex Account sign-in and indexes every server you own — no per-server config needed.

Server-backed indexes grade copies by **resolution, codec, and audio** (Atmos comes straight from the listing). With `deep_scan=True` the Plex backend also detects **HDR10/HLG/Dolby Vision** via per-item detail calls, cached persistently keyed by the item's `updatedAt` — so only never-seen items cost a request, and replacing a file (a quality upgrade) rescans that one item immediately (Jellyfin exposes the range in listings). Entries that matched a library copy carry the copy's quality in the `library_quality` field. Source is not exposed by the server APIs; upgrades are judged only on dimensions the library copy actually knows, so unknown-vs-known never counts as an upgrade.

The filesystem backend walks the configured paths and parses video filenames
with the same release-name parsers the pipeline uses, keeping the best
quality per episode/movie. The index is cached in memory and rebuilt when
older than `ttl`.

The `plex` and `jellyfin` backends build the same index from the media
server's API (`url` + `token`) instead of walking disk. Those APIs expose
resolution as the only reliable quality signal, so server backends compare
resolution alone; the filesystem backend, which parses release names, also
sees source/codec. If the server is unreachable at rescan time the previous
index is kept — an unreachable server never counts as an empty library.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `paths` | list | yes | — | Library directories to index (walked recursively) |
| `backend` | string | no | `filesystem` | `filesystem`, `plex`, or `jellyfin` |
| `ttl` | duration | no | `15m` | How long the disk index is reused before rescanning |
| `upgrade` | bool | no | `true` | Pass entries whose quality is strictly better than the library copy |
| `extensions` | list | no | common video types | File extensions to index (filesystem backend only) |
| `url` | string | plex/jellyfin | — | Media server base URL |
| `token` | string | plex/jellyfin | — | Media server API token |
| `sections` | list | no | — | Only index these server libraries, by name (e.g. `["Movies"]`). Plex/Jellyfin backends. Omit to index all of them. |
| `exclude_sections` | list | no | — | Index every server library except these, by name (e.g. `["3D Movies"]`). Plex/Jellyfin backends. |

## Matching

**By identity, where both sides have one:**

- **Episodes**: the show's TheTVDB id + episode ID. Plex and Jellyfin both
  publish the id on the show, and `series_gaps`, `discover` and the metainfo
  plugins all leave one on the entry.
- **Movies**: the film's TMDb or IMDb id, as the server publishes it.

**By name otherwise**, exactly as before:

- **Episodes**: normalized show name + episode ID (`series_episode_id` from
  `metainfo_file`). Decorated release titles are re-parsed as a fallback, so
  the filter also works before title cleanup.
- **Movies**: normalized title + year (`video_year` when present, else parsed
  from the release name).

Entries matching nothing in the library pass through untouched; the filter
never accepts, it only rejects (or lets upgrades through).

### Why the id comes first

A title is not stable identity, and this gate used to have nothing else. Plex's
own agent writes `Brothers (2026)` where the provider says `Brothers`; a server
and a provider disagree about a film's subtitle and punctuation more often than
they agree. The lookup then missed — and a miss here means *not owned*, so the
gate quietly did not fire at all for those items. On one real library, 33 of 323
shows carry a trailing `(YYYY)`.

It was also an inconsistency within a single pipeline: `series_gaps` has matched
the library by id since 1.57.0, so the scan deciding *which episodes are
missing* and the gate deciding *whether this release is already owned* were
reading the same library through two different keys.

**An id proves a hit, never a miss.** A release whose id the server does not
publish falls through to the name, so a wrong id — one that came from an
indexer's release metadata, say — can only fail to find something, never reject
the wrong thing. A server that publishes no ids at all behaves exactly as it did.

The filesystem backend has only filenames, so its id indexes stay empty and
every lookup there takes the name path.

## Example

```python
src  = input("rss", url="https://feeds.example.com/tv.rss")
meta = process("metainfo_file", upstream=src)
lib  = process("library", upstream=meta, paths=["/mnt/media/tv", "/mnt/media/movies"])
out  = output("transmission", upstream=lib, host="localhost")
pipeline("tv", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | `title` |

## Scoping to specific libraries

The index keys movies on title + year and episodes on show + episode id, with no record of which library an item came from — so without a filter, every movie library on the server is pooled into one answer. That is wrong whenever a server holds more than one: with both `Movies` and `3D Movies` present, a film owned *only* in 3D makes the filter reject a 2D release of it.

`sections` and `exclude_sections` scope the index by library name (case-insensitive; set one or the other, not both):

```python
lib = process("library", upstream=movies, backend="plex",
              sections=["Movies"], deep_scan=True)
```

Both need a `plex` or `jellyfin` backend — the filesystem backend already selects content through `paths`, so a section filter there would silently do nothing and is rejected at config time.

A server that cannot report its library names (an older Jellyfin build, a restricted token) leaves the name empty on every item. The filter then warns and indexes everything, because the alternatives are both worse: matching nothing would wave every duplicate through, and there is no safe way to guess which library an item belongs to.
