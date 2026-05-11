# Processor plugins (`plugins/processor/`)

Processor plugins transform entries. Use them with `process("plugin-name", from_=…)`.
They filter, enrich, or mutate entries and return what should continue downstream.

## Sub-directories

| Directory | Contains |
|-----------|---------|
| [`metainfo/`](metainfo/README.md) | Enrichment processors (quality, series info, TMDb, TVDB, Trakt, torrent metadata) |
| [`filter/`](filter/README.md) | Accept/reject processors (series, movies, seen, quality, condition, etc.) |
| [`modify/`](modify/README.md) | Field mutation processors (pathfmt, set) |
| [`discover/`](discover/README.md) | Active search processor |
