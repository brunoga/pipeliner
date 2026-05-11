# Metainfo plugins

Metainfo plugins implement `ProcessorPlugin` and enrich entries with extra fields (quality tags, series info, TMDb/TVDB data, torrent metadata, etc.). Place them upstream of any processors that consume the fields they produce.

Fields set by metainfo plugins are available in `condition` expressions, `pathfmt` / `set` patterns, and sink configs.

| Plugin | Description |
|--------|-------------|
| [metainfo_quality](quality/README.md) | Parse quality tags (resolution, source, codec) from the title |
| [metainfo_series](series/README.md) | Parse series name, season, and episode number from the title |
| [metainfo_tmdb](tmdb/README.md) | Enrich movie entries with TMDb metadata |
| [metainfo_tvdb](tvdb/README.md) | Enrich series entries with TheTVDB metadata |
| [metainfo_trakt](trakt/README.md) | Annotate entries with Trakt.tv metadata |
| [metainfo_torrent](torrent/README.md) | Read metadata from a `.torrent` file (info hash, size, file list) |
| [metainfo_magnet](magnet/README.md) | Annotate magnet-link entries with info hash, trackers, and DHT metadata |

## API caching

All external-API metainfo plugins (`metainfo_tmdb`, `metainfo_tvdb`, `metainfo_trakt`) cache results in SQLite. The cache is keyed by the search query and expires after `cache_ttl` (default 24h). Results survive process restarts, so a single API call covers many runs until the TTL expires.

## Torrent vs magnet

Use `metainfo_torrent` when entries are `.torrent` file URLs or local torrent files (set by `filesystem`). Use `metainfo_magnet` when entries are magnet links — it extracts the info hash and tracker list from the URI instantly, and optionally resolves the full name, size, and file list via DHT.
