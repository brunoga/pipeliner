# library

Rejects entries whose episode or movie already exists in the actual media
library on disk at equal-or-better quality. Unlike [`seen`](../seen/README.md)
(which knows what pipeliner grabbed), this checks **disk truth** — it catches
content acquired outside pipeliner, and it enables real quality upgrades: a
release strictly better than the library copy passes through (disable with
`upgrade=false`).

The filesystem backend walks the configured paths and parses video filenames
with the same release-name parsers the pipeline uses, keeping the best
quality per episode/movie. The index is cached in memory and rebuilt when
older than `ttl`. Remote backends (Plex, Jellyfin) are planned; the `backend`
key exists so configs stay stable when they arrive.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `paths` | list | yes | — | Library directories to index (walked recursively) |
| `backend` | string | no | `filesystem` | Only `filesystem` is supported today |
| `ttl` | duration | no | `15m` | How long the disk index is reused before rescanning |
| `upgrade` | bool | no | `true` | Pass entries whose quality is strictly better than the library copy |
| `extensions` | list | no | common video types | File extensions to index (`.mkv`, `.mp4`, `.avi`, `.m4v`, `.ts`, `.wmv`) |

## Matching

- **Episodes**: normalized show name + episode ID (`series_episode_id` from
  `metainfo_file`). Decorated release titles are re-parsed as a fallback, so
  the filter also works before title cleanup.
- **Movies**: normalized title + year (`video_year` when present, else parsed
  from the release name).
- Entries matching nothing in the library pass through untouched; the filter
  never accepts, it only rejects (or lets upgrades through).

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
